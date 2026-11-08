package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/claude"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// ClaudeUsageURL is the OAuth usage endpoint.
const ClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// Claude is the subscription usage source. The five-minute floor protects the rate-limited
// endpoint.
func Claude() *quota.Source {
	return &quota.Source{
		ID: "claude", Label: "Claude", Short: "cl", Aliases: []string{"cl", "cc"},
		ParserVersion: 2, MinPoll: 5 * time.Minute,
		Fetch:   fetchClaude(ClaudeUsageURL),
		Headers: claudeHeaders,
	}
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
			h := bearer(token)
			h.Set("anthropic-beta", "oauth-2025-04-20")
			h.Set("User-Agent", claude.UserAgent(claude.PinnedVersion))
			return getJSON(ctx, "claude", http.MethodGet, url, h, nil, token)
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
	return quota.Snapshot{Provider: "claude", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws}, nil
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
