package sources

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Codex URLs and credential names.
const (
	CodexUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	CodexResetURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	// CodexPiProvider is the PiG provider whose login carries a ChatGPT account claim.
	CodexPiProvider = "openai-codex"
	codexLoginHint  = "run /login " + CodexPiProvider
)

// CodexFile reads the pi-quotas Codex credential file as a read-only fallback; nil means none
// or unusable (expired tokens cannot be refreshed here).
type CodexFile func(now time.Time) *quota.Auth

// Codex reports ChatGPT subscription windows and reset credits (not OpenAI API billing).
func Codex(file CodexFile) *quota.Source {
	r := &codexResets{}
	return &quota.Source{
		ID: "codex", Label: "ChatGPT", Short: "cox", Aliases: []string{"openai", "openai-codex", "chatgpt", "cx", "cox", "oai"},
		ParserVersion: 2, Fetch: fetchCodex(CodexUsageURL), Headers: codexHeaders, Resets: r,
		AuthFrom: func(ctx context.Context, lookup func(string) (*quota.Auth, error)) (*quota.Auth, error) {
			a, err := lookup(CodexPiProvider)
			if err != nil || a != nil {
				return a, err
			}
			if file != nil {
				return file(time.Now()), nil
			}
			return nil, nil
		},
	}
}

// codexAccount reads the ChatGPT account claim from the token, else the explicit value.
func codexAccount(a *quota.Auth) string {
	parts := strings.Split(a.Token, ".")
	if len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims map[string]any
			if json.Unmarshal(raw, &claims) == nil {
				if id := text(obj(claims["https://api.openai.com/auth"])["chatgpt_account_id"]); id != "" {
					return id
				}
			}
		}
	}
	return a.AccountID
}

func codexHeaders2(a *quota.Auth) (http.Header, []string) {
	h := http.Header{"Authorization": {"Bearer " + a.Token}, "User-Agent": {"codex_cli_rs/0.60.0"}, "Originator": {"codex_cli_rs"}, "Accept": {"application/json"}}
	secrets := []string{a.Token}
	if id := codexAccount(a); id != "" {
		h.Set("ChatGPT-Account-Id", id)
		secrets = append(secrets, id)
	}
	return h, secrets
}

func codexNeedsLogin(now time.Time, reason string) quota.Snapshot {
	return needsAuth("codex", now, "codex: "+reason+"; "+codexLoginHint)
}

func apiKeyLike(a *quota.Auth) bool { return strings.HasPrefix(a.Token, "sk-") }

func fetchCodex(url string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		now := env.Now()
		if auth == nil {
			return codexNeedsLogin(now, "no credential configured"), nil
		}
		if apiKeyLike(auth) {
			return codexNeedsLogin(now, "subscription usage needs a ChatGPT login, not an API key"), nil
		}
		h, secrets := codexHeaders2(auth)
		body, err := getJSON(ctx, "codex", http.MethodGet, url, h, nil, secrets...)
		var se *httpx.StatusError
		if errors.As(err, &se) && (se.Status == 401 || se.Status == 403) {
			return codexNeedsLogin(now, fmt.Sprintf("GET %s rejected authentication (HTTP %d)", CodexUsageURL, se.Status)), nil
		}
		if err != nil {
			return quota.Snapshot{}, err
		}
		return ParseCodex(body, now)
	}
}

func codexWindow(id string, raw map[string]any, prefix string, important bool, now time.Time) quota.Window {
	w := quota.Window{ID: id, Important: important}
	if secs := num(raw["limit_window_seconds"]); secs != nil && *secs > 0 {
		if *secs >= 86400 {
			w.Period = fmt.Sprintf("%dd", int(math.Round(*secs/86400)))
		} else {
			w.Period = fmt.Sprintf("%dh", int(math.Max(1, math.Round(*secs/3600))))
		}
	}
	w.Short = firstNonEmpty(w.Period, id)
	if w.Period != "" {
		w.Label = fmt.Sprintf("%s (%s window)", prefix, w.Period)
	} else {
		w.Label = fmt.Sprintf("%s (%s)", prefix, id)
	}
	if strings.HasPrefix(id, "extra:") {
		w.Scope = strings.ToLower(prefix)
	}
	w.Used = percent(num(raw["used_percent"]))
	if w.ResetAtMs = timeMs(raw["reset_at"]); w.ResetAtMs == nil {
		if after := num(raw["reset_after_seconds"]); after != nil {
			w.ResetAtMs = quota.Ms(now.UnixMilli() + int64(*after*1000))
		}
	}
	return w
}

// ParseCodex reads the usage payload without copying account identifiers into the snapshot.
func ParseCodex(body map[string]any, now time.Time) (quota.Snapshot, error) {
	var ws []quota.Window
	limits := obj(body["rate_limit"])
	if p := obj(limits["primary_window"]); p != nil {
		ws = append(ws, codexWindow("primary", p, "Plan", true, now))
	}
	if s := obj(limits["secondary_window"]); s != nil {
		ws = append(ws, codexWindow("secondary", s, "Plan", len(ws) == 0, now))
	}
	if extras, ok := body["additional_rate_limits"].([]any); ok {
		for _, v := range extras {
			e := obj(v)
			name := firstNonEmpty(text(e["limit_name"]), text(e["metered_feature"]), "extra")
			if p := obj(obj(e["rate_limit"])["primary_window"]); p != nil {
				ws = append(ws, codexWindow("extra:"+name, p, name, false, now))
			}
		}
	}
	var credits *int
	if c := num(obj(body["rate_limit_reset_credits"])["available_count"]); c != nil && *c >= 0 {
		credits = quota.Int(int(*c))
	}
	hasUsed := false
	for _, w := range ws {
		hasUsed = hasUsed || w.Used != nil
	}
	if !hasUsed && credits == nil {
		return quota.Snapshot{}, fmt.Errorf("codex: GET %s missing usable used_percent and rate_limit_reset_credits.available_count; retry or check the usage endpoint schema", CodexUsageURL)
	}
	return quota.Snapshot{Provider: "codex", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Tier: text(body["plan_type"]), Windows: ws, ResetCredits: credits}, nil
}

// codexHeaders reads x-codex-* response headers (keys lower-cased).
func codexHeaders(h map[string]string, now time.Time) *quota.Snapshot {
	var ws []quota.Window
	for _, id := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + id
		rawUsed, hasUsed := h[prefix+"-used-percent"]
		rawMin, hasMin := h[prefix+"-window-minutes"]
		rawReset, hasReset := h[prefix+"-reset-at"]
		if !hasUsed && !hasMin && !hasReset {
			continue
		}
		used, minutes, reset := numish(rawUsed), numish(rawMin), timeMs(rawReset)
		// Codex emits zeroed placeholders for absent windows, including an empty reset-at.
		if used != nil && *used == 0 && (minutes == nil || *minutes == 0) && reset == nil {
			continue
		}
		raw := map[string]any{}
		if used != nil {
			raw["used_percent"] = *used
		}
		if minutes != nil {
			raw["limit_window_seconds"] = *minutes * 60
		}
		if hasReset {
			raw["reset_at"] = rawReset
		}
		ws = append(ws, codexWindow(id, raw, "Plan", len(ws) == 0, now))
	}
	if len(ws) == 0 {
		return nil
	}
	return &quota.Snapshot{Provider: "codex", FetchedAt: now.UnixMilli(), Source: quota.FromHeaders, Tier: strings.TrimSpace(h["x-codex-plan-type"]), Windows: ws}
}

// codexResets lists and spends reset credits.
type codexResets struct{}

const (
	checkList     = "check /quotas reset codex before retrying"
	possiblySpent = "the credit may already be spent; " + checkList
)

// ParseCodexResets reads the reset inventory, counting available credits itself only when
// the reported count is invalid.
func ParseCodexResets(body map[string]any, now time.Time) (quota.ResetList, error) {
	raw, ok := body["credits"].([]any)
	if !ok {
		return quota.ResetList{}, fmt.Errorf("codex: GET %s has no credits[] array; check the reset endpoint schema", CodexResetURL)
	}
	credits := make([]quota.ResetCredit, 0, len(raw))
	for _, v := range raw {
		m := obj(v)
		id, status := text(m["id"]), text(m["status"])
		if id == "" || status == "" {
			continue
		}
		c := quota.ResetCredit{ID: id, Status: status, Type: firstNonEmpty(text(m["reset_type"]), "unknown"), Title: text(m["title"]), Description: text(m["description"]),
			GrantedAtMs: timeMs(m["granted_at"]), ExpiresAtMs: timeMs(m["expires_at"]), RedeemedAtMs: timeMs(m["redeemed_at"])}
		if b, ok := m["is_supported_by_plan"].(bool); ok {
			c.SupportedByPlan = &b
		}
		credits = append(credits, c)
	}
	available := 0
	if n := num(body["available_count"]); n != nil && *n >= 0 && *n == math.Trunc(*n) {
		available = int(*n)
	} else {
		for _, c := range credits {
			if c.Status == "available" {
				available++
			}
		}
	}
	return quota.ResetList{Provider: "codex", FetchedAt: now.UnixMilli(), Available: available, Credits: credits}, nil
}

func (codexResets) credential(ctx context.Context, env quota.Env) (http.Header, []string, error) {
	a, err := env.Auth(ctx)
	if err != nil {
		return nil, nil, err
	}
	if a == nil || apiKeyLike(a) {
		return nil, nil, fmt.Errorf("codex: reset credits require a ChatGPT login; %s", codexLoginHint)
	}
	h, secrets := codexHeaders2(a)
	return h, secrets, nil
}

func resetFailure(se *httpx.StatusError, method, url string, consume bool) error {
	hint := checkList
	switch {
	case se.Status == 401 || se.Status == 403:
		hint = codexLoginHint
	case consume && se.Status >= 500:
		hint = possiblySpent
	}
	body := strings.Join(strings.Fields(se.Body), " ")
	if len(body) > 200 {
		body = body[:200]
	}
	msg := fmt.Sprintf("codex: %s %s returned HTTP %d; %s", method, url, se.Status, hint)
	if body != "" {
		msg += ": " + body
	}
	return errors.New(msg)
}

func (r codexResets) List(ctx context.Context, env quota.Env) (quota.ResetList, error) {
	h, secrets, err := r.credential(ctx, env)
	if err != nil {
		return quota.ResetList{}, err
	}
	body, err := getJSON(ctx, "codex", http.MethodGet, CodexResetURL, h, nil, secrets...)
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return quota.ResetList{}, resetFailure(se, "GET", CodexResetURL, false)
	}
	if err != nil {
		return quota.ResetList{}, err
	}
	return ParseCodexResets(body, env.Now())
}

// ParseCodexOutcome reads a consume verdict; an unknown answer is never treated as success.
func ParseCodexOutcome(body map[string]any, creditID string) (quota.ResetOutcome, error) {
	code := quota.ResetCode(text(body["code"]))
	switch code {
	case quota.ResetDone, quota.NothingToReset, quota.NoCredit, quota.AlreadyRedeemed:
	default:
		return quota.ResetOutcome{}, fmt.Errorf("codex: POST %s returned an unrecognized result; %s", CodexResetURL+"/consume", possiblySpent)
	}
	windows := 0
	if n := num(body["windows_reset"]); n != nil && *n >= 0 {
		windows = int(*n)
	}
	return quota.ResetOutcome{Provider: "codex", CreditID: creditID, Code: code, WindowsReset: windows}, nil
}

// Consume sends exactly one request: redeem_request_id idempotency is unverified, so a retry
// could spend a second credit.
func (r codexResets) Consume(ctx context.Context, env quota.Env, creditID string) (quota.ResetOutcome, error) {
	h, secrets, err := r.credential(ctx, env)
	if err != nil {
		return quota.ResetOutcome{}, err
	}
	h.Set("Content-Type", "application/json")
	reqBody, _ := json.Marshal(map[string]string{"redeem_request_id": uuid(), "credit_id": creditID})
	url := CodexResetURL + "/consume"
	body, err := getJSON(ctx, "codex", http.MethodPost, url, h, reqBody, secrets...)
	var se *httpx.StatusError
	switch {
	case errors.As(err, &se):
		return quota.ResetOutcome{}, resetFailure(se, "POST", url, true)
	case err != nil && ctx.Err() == nil:
		return quota.ResetOutcome{}, fmt.Errorf("codex: POST %s did not complete; %s", url, possiblySpent)
	case err != nil:
		return quota.ResetOutcome{}, fmt.Errorf("codex: POST %s was interrupted; %s", url, possiblySpent)
	}
	return ParseCodexOutcome(body, creditID)
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
