package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/claude"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Claude URLs. The usage poll asks for the cedar_ember grant block; the reset endpoint is
// per organization, so its URL is a template and the organization is resolved per consume.
const (
	ClaudeUsageURL   = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1"
	ClaudeProfileURL = "https://api.anthropic.com/api/oauth/profile"
	ClaudeOrgsURL    = "https://api.anthropic.com/api/organizations/"
	// ClaudeResetURL names the reset endpoint as reported to the user; the organization id
	// never appears in text.
	ClaudeResetURL      = ClaudeOrgsURL + "<organization>/reset_rate_limits"
	claudeResetPath     = "/reset_rate_limits"
	claudeLoginHint     = "run /login claude"
	claudeCheckList     = "check /quotas reset claude before approving another attempt"
	claudePossiblySpent = "the credit may already be spent; " + claudeCheckList
)

// Claude is the subscription usage source. The five-minute floor protects the rate-limited
// endpoint.
func Claude() *quota.Source {
	return &quota.Source{
		ID: "claude", Label: "Claude", Short: "cl", Aliases: []string{"cl", "cc"},
		ParserVersion: 3, MinPoll: 5 * time.Minute,
		Fetch:   fetchClaude(ClaudeUsageURL),
		Headers: claudeHeaders,
		Resets:  claudeResets{usageURL: ClaudeUsageURL, profileURL: ClaudeProfileURL, orgsURL: ClaudeOrgsURL},
		// The usage endpoint rejects API keys; only the subscription OAuth token reads it.
		AuthFrom: func(_ context.Context, lookup func(string) (*quota.Auth, error)) (*quota.Auth, error) {
			a, err := lookup("claude")
			if err != nil || a == nil || !strings.HasPrefix(a.Token, "sk-ant-oat") {
				return nil, err
			}
			return a, nil
		},
	}
}

// claudeQuotaHeaders are the headers the OAuth usage, profile and reset endpoints share.
func claudeQuotaHeaders(token string) http.Header {
	h := bearer(token)
	h.Set("anthropic-beta", "oauth-2025-04-20")
	h.Set("User-Agent", claude.UserAgent(claude.PinnedVersion))
	return h
}

func fetchClaude(url string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		if auth == nil {
			return needsAuth("claude", env.Now(), "Claude: no credential configured; run /login claude"), nil
		}
		get := func(token string) (map[string]any, error) {
			return getJSON(ctx, "claude", http.MethodGet, url, claudeQuotaHeaders(token), nil, token)
		}
		body, err := get(auth.Token)
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
			// Replay only when PiG resolved a different credential meanwhile.
			if fresh, aerr := env.Auth(ctx); aerr == nil && fresh != nil && fresh.Token != auth.Token {
				body, err = get(fresh.Token)
			}
		}
		if err != nil {
			return quota.Snapshot{}, contextual(err)
		}
		return ParseClaude(body, env.Now())
	}
}

// contextual says what to do about a failed usage request.
func contextual(err error) error {
	var se *httpx.StatusError
	if !errors.As(err, &se) {
		return err
	}
	switch se.Status {
	case http.StatusUnauthorized:
		se.Body = "Claude usage; run /login claude"
	case http.StatusForbidden, http.StatusTooManyRequests:
		se.Body = "Claude usage; rate limited; retry after the Retry-After hold expires"
	default:
		se.Body = "Claude usage; retry later; check the provider service"
	}
	return se
}

type claudeWindow struct {
	key, id, short, label, period, scope string
	important                            bool
}

var claudeLegacy = []claudeWindow{
	{"five_hour", "5h", "5h", "5h session", "5h", "", true},
	{"seven_day", "7d", "7d", "7d weekly", "7d", "", true},
	{"seven_day_opus", "scoped:opus", "opu", "7d Opus", "7d", "Opus", false},
	{"seven_day_sonnet", "scoped:sonnet", "son", "7d Sonnet", "7d", "Sonnet", false},
	{"seven_day_oauth_apps", "scoped:oauth apps", "oau", "7d OAuth apps", "7d", "OAuth apps", false},
}

func displayName(v any) string { return text(obj(v)["display_name"]) }

func claudeLimit(raw any) (quota.Window, bool) {
	m := obj(raw)
	kind, group, severity := text(m["kind"]), text(m["group"]), text(m["severity"])
	active, isBool := m["is_active"].(bool)
	reset, hasReset := m["resets_at"]
	if kind == "" || group == "" || severity == "" || !isBool {
		return quota.Window{}, false
	}
	if _, isStr := reset.(string); hasReset && reset != nil && !isStr {
		return quota.Window{}, false
	}
	name := ""
	if scope := obj(m["scope"]); scope != nil {
		if name = displayName(scope["model"]); name == "" {
			name = displayName(scope["surface"])
		}
	}
	w := quota.Window{ID: kind + ":" + group, Short: strings.ToLower(trunc(kind, 3))}
	w.Label = strings.TrimSpace(strings.Join(nonEmpty(kind, group, name), " "))
	switch kind {
	case "session":
		w.ID, w.Short, w.Label, w.Period = "5h", "5h", "5h session", "5h"
	case "weekly_all":
		w.ID, w.Short, w.Label, w.Period = "7d", "7d", "7d weekly", "7d"
	case "weekly_scoped":
		if name == "" {
			name = "Scoped"
		}
		w.ID, w.Short, w.Label, w.Period = "scoped:"+strings.ToLower(name), strings.ToLower(trunc(name, 3)), "7d "+name, "7d"
	}
	w.Scope = name
	w.Used = percent(num(m["percent"]))
	w.ResetAtMs = timeMs(reset)
	w.Important = kind == "session" || kind == "weekly_all" || active || severity != "normal"
	return w, true
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseClaude reads the usage payload without retaining profile, account or share data.
func ParseClaude(body map[string]any, now time.Time) (quota.Snapshot, error) {
	var ws []quota.Window
	limits, hasLimits := body["limits"].([]any)
	extra := obj(body["extra_usage"])
	_, extraFlag := extra["is_enabled"].(bool)
	recognized := hasLimits || extraFlag
	if hasLimits && len(limits) > 0 {
		seen := map[string]bool{}
		for i, raw := range limits {
			w, ok := claudeLimit(raw)
			if !ok {
				continue
			}
			if seen[w.ID] {
				w.ID = fmt.Sprintf("%s:%d", w.ID, i)
			}
			seen[w.ID] = true
			ws = append(ws, w)
		}
	} else {
		for _, l := range claudeLegacy {
			b := obj(body[l.key])
			if b == nil {
				continue
			}
			recognized = true
			ws = append(ws, quota.Window{ID: l.id, Short: l.short, Label: l.label, Period: l.period, Scope: l.scope, Important: l.important,
				Used: percent(num(b["utilization"])), ResetAtMs: timeMs(b["resets_at"])})
		}
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := obj(body[key])
		cap := num(b["limit_dollars"])
		if cap == nil {
			continue
		}
		recognized = true
		pct := num(b["utilization"])
		used := num(b["used_dollars"])
		label := text(b["label"])
		if label == "" {
			label = fmt.Sprintf("$%.0f cap", *cap)
		}
		w := quota.Window{ID: key, Short: "cap", Label: label, Scope: label, Used: percent(pct), ResetAtMs: timeMs(b["resets_at"]), Important: pct != nil && *pct >= 90}
		if used != nil {
			w.Value = fmt.Sprintf("$%.2f / $%.0f", *used, *cap)
		}
		ws = append(ws, w)
	}
	if extra != nil && extra["is_enabled"] == true {
		cap, used, pct := num(extra["monthly_limit"]), num(extra["used_credits"]), num(extra["utilization"])
		if cap != nil && *cap > 0 {
			cur := strings.ToUpper(text(extra["currency"]))
			if cur == "" {
				cur = "USD"
			}
			prefix := cur + " "
			if cur == "USD" {
				prefix = "$"
			}
			w := quota.Window{ID: "extra_usage", Short: "ext", Label: "Extra usage cap", Period: "1mo", Scope: "extra usage"}
			switch {
			case used != nil:
				w.Used = frac(quota.Frac(*used / *cap))
				w.Value = fmt.Sprintf("%s%.2f / %s%.2f", prefix, *used/100, prefix, *cap/100)
			case pct != nil:
				w.Used = percent(pct)
			}
			ws = append(ws, w)
		}
	}
	if !recognized || (hasLimits && len(limits) > 0 && len(ws) == 0) {
		return quota.Snapshot{}, fmt.Errorf("Claude usage (GET %s): missing recognized quota windows or extra_usage; retry later", ClaudeUsageURL)
	}
	return quota.Snapshot{Provider: "claude", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws, ResetCredits: claudeResetCount(body, now)}, nil
}

// claudeResetCount keeps an unreported or malformed grant block unknown; it never fails the
// window poll.
func claudeResetCount(body map[string]any, now time.Time) *int {
	if body["cedar_ember"] == nil {
		return nil
	}
	l, err := ParseClaudeResets(body, now)
	if err != nil {
		return nil
	}
	return quota.Int(l.Available)
}

// claudeGrantID accepts the grant ids the reset endpoint is addressed with.
var claudeGrantID = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// claudeOrgID accepts the organization uuid the profile reports; it is never stored or logged.
var claudeOrgID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func textList(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s := text(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ParseClaudeResets reads the cedar_ember grants in server order. Only usable, unpaused
// grants count as available, and only the server's next grant is redeemable.
func ParseClaudeResets(body map[string]any, now time.Time) (quota.ResetList, error) {
	list := quota.ResetList{Provider: "claude", FetchedAt: now.UnixMilli(), Credits: []quota.ResetCredit{}}
	grant, present := body["cedar_ember"]
	if !present || grant == nil {
		return list, nil
	}
	plan := obj(grant)
	if plan == nil {
		return quota.ResetList{}, fmt.Errorf("claude: GET %s cedar_ember is not an object; %s", ClaudeUsageURL, claudeCheckList)
	}
	raw, ok := plan["grants"].([]any)
	if !ok {
		return quota.ResetList{}, fmt.Errorf("claude: GET %s cedar_ember has no grants[]; %s", ClaudeUsageURL, claudeCheckList)
	}
	next := text(plan["next_grant_id"])
	for _, v := range raw {
		g := obj(v)
		id, left := text(g["id"]), num(g["resets_left"])
		if !claudeGrantID.MatchString(id) || left == nil || *left < 1 || *left != math.Trunc(*left) {
			continue
		}
		status := "unavailable"
		if g["usable_now"] == true && g["paused"] == false {
			list.Available += int(*left)
			// Only the server's next grant is redeemable; the other usable grants queue behind it.
			status = "queued"
			if id == next {
				status = "available"
			}
		}
		c := quota.ResetCredit{ID: id, Type: firstNonEmpty(text(g["type"]), "unknown"), Status: status, Title: text(g["label"]),
			GrantedAtMs: timeMs(g["starts_at"]), ExpiresAtMs: timeMs(g["ends_at"])}
		if clears := textList(g["clears"]); len(clears) > 0 {
			c.Description = "clears " + strings.Join(clears, ", ")
		}
		list.Credits = append(list.Credits, c)
	}
	return list, nil
}

// ParseClaudeOutcome maps one claim verdict; only result "reset" confirms a spend, and every
// other answer, including an unknown one, is a refusal carrying the provider's reason.
func ParseClaudeOutcome(body map[string]any, creditID string) (quota.ResetOutcome, error) {
	result := text(body["result"])
	if result == "" {
		return quota.ResetOutcome{}, fmt.Errorf("claude: POST %s returned no reset result; %s", ClaudeResetURL, claudePossiblySpent)
	}
	code := quota.Refused
	switch result {
	case "reset":
		code = quota.ResetDone
	case "already_used":
		code = quota.AlreadyRedeemed
	case "not_limited":
		code = quota.NothingToReset
	}
	o := quota.ResetOutcome{Provider: "claude", CreditID: creditID, Code: code, Reason: text(body["reason"])}
	if code == quota.Refused && o.Reason == "" {
		o.Reason = result
	}
	if code == quota.ResetDone {
		o.WindowsReset = len(textList(body["cleared"]))
	}
	return o, nil
}

// claudeResets lists grants from the usage poll and spends exactly one per consume request.
type claudeResets struct {
	usageURL, profileURL, orgsURL string
}

func (r claudeResets) credential(ctx context.Context, env quota.Env) (*quota.Auth, error) {
	a, err := env.Auth(ctx)
	if err != nil {
		return nil, err
	}
	if a == nil || !strings.HasPrefix(a.Token, "sk-ant-oat") {
		return nil, fmt.Errorf("claude: reset grants require the subscription OAuth login, not an API key; %s", claudeLoginHint)
	}
	return a, nil
}

// claudeResetFailure names the recovery without repeating the organization id: the displayed
// endpoint is a template, never the addressed URL.
func claudeResetFailure(se *httpx.StatusError, method, url string, posting bool) error {
	hint := claudeCheckList
	switch {
	case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden:
		hint = claudeLoginHint
	case se.Status == http.StatusTooManyRequests:
		hint = "rate limited; wait for the Retry-After hold. " + claudeCheckList
	case posting:
		hint = claudePossiblySpent
	}
	body := strings.Join(strings.Fields(se.Body), " ")
	if len(body) > 200 {
		body = body[:200]
	}
	msg := fmt.Sprintf("claude: %s %s returned HTTP %d; %s", method, url, se.Status, hint)
	if body != "" {
		msg += ": " + body
	}
	return errors.New(msg)
}

func (r claudeResets) List(ctx context.Context, env quota.Env) (quota.ResetList, error) {
	a, err := r.credential(ctx, env)
	if err != nil {
		return quota.ResetList{}, err
	}
	body, err := getJSON(ctx, "claude", http.MethodGet, r.usageURL, claudeQuotaHeaders(a.Token), nil, a.Token)
	var se *httpx.StatusError
	if errors.As(err, &se) {
		// The grant block shares the usage poll, so a 403 is the same rate-limit hold.
		return quota.ResetList{}, contextual(se)
	}
	if err != nil {
		return quota.ResetList{}, err
	}
	return ParseClaudeResets(body, env.Now())
}

// Consume resolves the organization transiently and sends exactly one claim: the request id is
// not a verified idempotency key, so a retry could spend a second grant.
func (r claudeResets) Consume(ctx context.Context, env quota.Env, creditID string) (quota.ResetOutcome, error) {
	if !claudeGrantID.MatchString(creditID) {
		return quota.ResetOutcome{}, fmt.Errorf("claude: invalid reset grant id; %s", claudeCheckList)
	}
	a, err := r.credential(ctx, env)
	if err != nil {
		return quota.ResetOutcome{}, err
	}
	secrets := []string{a.Token}
	profile, err := callJSON(ctx, "claude", http.MethodGet, r.profileURL, claudeQuotaHeaders(a.Token), nil, secrets, true)
	var se *httpx.StatusError
	switch {
	case errors.As(err, &se):
		return quota.ResetOutcome{}, claudeResetFailure(se, "GET", ClaudeProfileURL, false)
	case err != nil && ctx.Err() == nil:
		return quota.ResetOutcome{}, fmt.Errorf("claude: GET %s did not complete; %s", ClaudeProfileURL, claudeCheckList)
	case err != nil:
		return quota.ResetOutcome{}, fmt.Errorf("claude: GET %s was interrupted; %s", ClaudeProfileURL, claudeCheckList)
	}
	org := text(obj(obj(profile)["organization"])["uuid"])
	if !claudeOrgID.MatchString(org) {
		return quota.ResetOutcome{}, fmt.Errorf("claude: profile has no valid organization; %s", claudeLoginHint)
	}
	if err := ctx.Err(); err != nil {
		return quota.ResetOutcome{}, fmt.Errorf("claude: the claim was cancelled before it was sent; nothing was spent")
	}
	h := claudeQuotaHeaders(a.Token)
	h.Set("Content-Type", "application/json")
	payload, _ := json.Marshal(map[string]string{"program": "cedar_ember", "grant_id": creditID, "request_id": uuid()})
	secrets = append(secrets, org)
	body, err := callJSON(ctx, "claude", http.MethodPost, r.orgsURL+org+claudeResetPath, h, payload, secrets, true)
	switch {
	case errors.As(err, &se):
		return quota.ResetOutcome{}, claudeResetFailure(se, "POST", ClaudeResetURL, true)
	case err != nil && ctx.Err() == nil:
		return quota.ResetOutcome{}, fmt.Errorf("claude: POST %s did not complete; %s", ClaudeResetURL, claudePossiblySpent)
	case err != nil:
		return quota.ResetOutcome{}, fmt.Errorf("claude: POST %s was interrupted; %s", ClaudeResetURL, claudePossiblySpent)
	}
	o, err := ParseClaudeOutcome(body, creditID)
	if err != nil {
		return quota.ResetOutcome{}, err
	}
	o.Reason = httpx.Redact(strings.Join(strings.Fields(o.Reason), " "), secrets...)
	if len(o.Reason) > 200 {
		o.Reason = o.Reason[:200]
	}
	return o, nil
}

var claudeHeaderWindows = []claudeWindow{
	{"", "5h", "5h", "5h session", "5h", "", true},
	{"", "7d", "7d", "7d weekly", "7d", "", true},
	{"", "7d_oi", "7d_oi", "7d overage included", "7d", "overage included", false},
	{"", "overage", "cap", "Spend cap", "", "spend cap", false},
}

// claudeHeaders reads anthropic-ratelimit-unified-* response headers (keys lower-cased).
func claudeHeaders(h map[string]string, now time.Time) *quota.Snapshot {
	var ws []quota.Window
	for _, m := range claudeHeaderWindows {
		prefix := "anthropic-ratelimit-unified-" + m.id
		raw, hasUsed := h[prefix+"-utilization"]
		reset, hasReset := h[prefix+"-reset"]
		if !hasUsed && !hasReset {
			continue
		}
		w := quota.Window{ID: m.id, Short: m.short, Label: m.label, Period: m.period, Scope: m.scope, Important: m.important}
		if hasUsed {
			w.Used = frac(numish(raw))
		}
		if hasReset {
			w.ResetAtMs = timeMs(reset)
		}
		ws = append(ws, w)
	}
	if len(ws) == 0 {
		return nil
	}
	return &quota.Snapshot{Provider: "claude", FetchedAt: now.UnixMilli(), Source: quota.FromHeaders, Windows: ws}
}
