package sources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Billing endpoints.
const (
	XAICreditsURL     = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	XAIMonthlyURL     = "https://cli-chat-proxy.grok.com/v1/billing"
	OpenRouterCredits = "https://openrouter.ai/api/v1/credits"
	OpenRouterKeyURL  = "https://openrouter.ai/api/v1/key"
	DeepSeekBalance   = "https://api.deepseek.com/user/balance"
)

// both runs two requests concurrently and returns their results in order.
func both[T any](a, b func() (T, error)) (ra, rb T, ea, eb error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ra, ea = a() }()
	go func() { defer wg.Done(); rb, eb = b() }()
	wg.Wait()
	return
}

// XAI reports the included period and the on-demand and monthly caps from the Grok CLI billing
// service.
func XAI() *quota.Source {
	return &quota.Source{ID: "xai", Label: "xAI", Short: "xai", Aliases: []string{"grok"}, ParserVersion: 1, Fetch: fetchXAI(XAICreditsURL, XAIMonthlyURL)}
}

func fetchXAI(creditsURL, monthlyURL string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		now := env.Now()
		if auth == nil {
			return needsAuth("xai", now, "xai: no credential configured; run /login xai"), nil
		}
		load := func(url string) func() (map[string]any, error) {
			return func() (map[string]any, error) {
				return getJSON(ctx, "xai", http.MethodGet, url, bearer(auth.Token), nil, auth.Token)
			}
		}
		credits, monthly, ec, em := both(load(creditsURL), load(monthlyURL))
		if ctx.Err() != nil {
			return quota.Snapshot{}, ctx.Err()
		}
		snap := ParseXAI(credits, monthly, now)
		if len(snap.Windows) > 0 {
			return snap, nil
		}
		for _, e := range []error{ec, em} {
			var se *httpx.StatusError
			if errors.As(e, &se) {
				return quota.Snapshot{}, e
			}
		}
		return quota.Snapshot{}, fmt.Errorf("xai: no usable billing quota from %s or %s: missing currentPeriod.end, positive onDemandCap.val or positive monthlyLimit.val (or invalid JSON/transport failure); retry billing quotas later", creditsURL, monthlyURL)
	}
}

func fraction(used *float64, limit float64) *float64 {
	if used == nil {
		return nil
	}
	return quota.Frac(math.Max(0, math.Min(1, *used/limit)))
}

func dollars(used *float64, limit float64) string {
	if used == nil {
		return ""
	}
	return fmt.Sprintf("$%.2f / $%.2f", *used, limit)
}

// ParseXAI reads both billing bodies; either may be nil. An omitted usage percentage means
// unused only while the period is still active; afterwards it is unknown.
func ParseXAI(creditsBody, monthlyBody map[string]any, now time.Time) quota.Snapshot {
	credits, monthly := obj(obj(creditsBody)["config"]), obj(obj(monthlyBody)["config"])
	period := obj(credits["currentPeriod"])
	var ws []quota.Window
	if endText := text(period["end"]); endText != "" {
		end := timeMs(period["end"])
		pt := strings.ToUpper(text(period["type"]))
		weekly, month := strings.Contains(pt, "WEEK"), strings.Contains(pt, "MONTH")
		w := quota.Window{ID: "period", Short: "per", Label: "Current period", Scope: "current period", ResetAtMs: end, Important: true}
		switch {
		case weekly:
			w.ID, w.Short, w.Label, w.Period, w.Scope = "7d", "7d", "7d weekly quota", "7d", ""
		case month:
			w.Period, w.Scope = "1mo", ""
		}
		if p := num(credits["creditUsagePercent"]); p != nil {
			w.Used = percent(p)
		} else if end != nil && now.UnixMilli() < *end {
			w.Used = quota.Frac(0)
		}
		ws = append(ws, w)
	}
	if cap := num(obj(credits["onDemandCap"])["val"]); cap != nil && *cap > 0 {
		used := num(obj(credits["onDemandUsed"])["val"])
		ws = append(ws, quota.Window{ID: "ondemand", Short: "cap", Label: "On-demand cap", Scope: "on-demand cap", Used: fraction(used, *cap), Value: dollars(used, *cap)})
	}
	if limit := num(obj(monthly["monthlyLimit"])["val"]); limit != nil && *limit > 0 {
		used := num(obj(monthly["used"])["val"])
		ws = append(ws, quota.Window{ID: "monthly", Short: "mo", Label: "Monthly included", Period: "1mo", Scope: "included", Used: fraction(used, *limit),
			Value: dollars(used, *limit), ResetAtMs: timeMs(monthly["billingPeriodEnd"]), Important: len(ws) == 0})
	}
	snap := quota.Snapshot{Provider: "xai", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws}
	if b, _ := credits["isUnifiedBillingUser"].(bool); b {
		snap.Tier = "unified"
	}
	return snap
}

// OpenRouter reports the account balance and the key spend cap.
func OpenRouter() *quota.Source {
	return &quota.Source{ID: "openrouter", Label: "OpenRouter", Short: "or", Aliases: []string{"or"}, Credits: true, ParserVersion: 1, Fetch: fetchOpenRouter(OpenRouterCredits, OpenRouterKeyURL)}
}

func fetchOpenRouter(creditsURL, keyURL string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		if auth == nil {
			return needsAuth("openrouter", env.Now(), "OpenRouter: no credential configured; use /login openrouter"), nil
		}
		load := func(url string) func() (map[string]any, error) {
			return func() (map[string]any, error) {
				return getJSON(ctx, "openrouter", http.MethodGet, url, bearer(auth.Token), nil, auth.Token)
			}
		}
		credits, key, ec, ek := both(load(creditsURL), load(keyURL))
		if err := errors.Join(ec, ek); err != nil {
			return quota.Snapshot{}, firstStatus(ec, ek)
		}
		return ParseOpenRouter(credits, key, creditsURL, keyURL, env.Now())
	}
}

// firstStatus prefers an HTTP failure so the engine can pick the right backoff.
func firstStatus(errs ...error) error {
	for _, e := range errs {
		var se *httpx.StatusError
		if errors.As(e, &se) {
			return e
		}
	}
	return errors.Join(errs...)
}

// ParseOpenRouter reads both envelopes without retaining key or account identifiers.
func ParseOpenRouter(creditsBody, keyBody map[string]any, creditsURL, keyURL string, now time.Time) (quota.Snapshot, error) {
	credits, key := obj(obj(creditsBody)["data"]), obj(obj(keyBody)["data"])
	total, used := num(credits["total_credits"]), num(credits["total_usage"])
	limit, keyRemaining := num(key["limit"]), num(key["limit_remaining"])
	var details []string
	for _, d := range [][2]string{{"usage_daily", "day"}, {"usage_weekly", "wk"}, {"usage_monthly", "mo"}} {
		if u := num(key[d[0]]); u != nil {
			details = append(details, fmt.Sprintf("%s $%.2f", d[1], *u))
		}
	}
	if total == nil && used == nil && keyRemaining == nil && len(details) == 0 {
		return quota.Snapshot{}, fmt.Errorf("OpenRouter: %s and %s returned no numeric account credits/usage, key remaining cap or daily/weekly/monthly usage; retry quota refresh", creditsURL, keyURL)
	}
	var accountRemaining *float64
	if total != nil && used != nil {
		accountRemaining = quota.Frac(math.Max(0, *total-*used))
	}
	remaining := accountRemaining
	if remaining == nil && keyRemaining != nil {
		remaining = quota.Frac(math.Max(0, *keyRemaining))
	}
	var ws []quota.Window
	if total != nil && *total > 0 && used != nil && accountRemaining != nil {
		ws = append(ws, quota.Window{ID: "credits", Short: "cred", Label: "Account credits", Used: quota.Frac(clamp(*used / *total)),
			Value: fmt.Sprintf("$%.2f left of $%.2f", *accountRemaining, *total), Important: true})
	}
	if limit != nil && *limit > 0 && keyRemaining != nil {
		ws = append(ws, quota.Window{ID: "key-cap", Short: "key", Label: "Key spend cap", Scope: "key cap", Used: quota.Frac(clamp((*limit - *keyRemaining) / *limit)),
			Value: fmt.Sprintf("$%.2f left of $%.2f", *keyRemaining, *limit)})
	}
	return quota.Snapshot{Provider: "openrouter", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws,
		Credits: &quota.Balance{Remaining: remaining, Total: total, Used: used, Currency: "$", Detail: strings.Join(details, " · ")}}, nil
}

func clamp(f float64) float64 { return math.Max(0, math.Min(1, f)) }

// DeepSeek reports the prepaid balance in USD or CNY.
func DeepSeek() *quota.Source {
	return &quota.Source{ID: "deepseek", Label: "DeepSeek", Short: "ds", Aliases: []string{"ds"}, Credits: true, ParserVersion: 1, Fetch: fetchDeepSeek(DeepSeekBalance)}
}

func fetchDeepSeek(url string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		if auth == nil {
			return needsAuth("deepseek", env.Now(), "deepseek: no credential configured; use /login deepseek"), nil
		}
		body, err := getJSON(ctx, "deepseek", http.MethodGet, url, bearer(auth.Token), nil, auth.Token)
		if err != nil {
			return quota.Snapshot{}, err
		}
		return ParseDeepSeek(body, url, env.Now())
	}
}

// ParseDeepSeek prefers the USD entry; unknown amounts stay unknown rather than zero.
func ParseDeepSeek(body map[string]any, url string, now time.Time) (quota.Snapshot, error) {
	infos, _ := body["balance_infos"].([]any)
	var info map[string]any
	for _, v := range infos {
		m := obj(v)
		if m == nil {
			continue
		}
		if info == nil {
			info = m
		}
		if m["currency"] == "USD" {
			info = m
			break
		}
	}
	code := text(info["currency"])
	if info == nil || (code != "USD" && code != "CNY") {
		return quota.Snapshot{}, fmt.Errorf("deepseek: %s missing balance_infos with a supported currency (USD/CNY); retry the balance request", url)
	}
	amount := func(k string) *float64 { return numish(info[k]) }
	remaining, granted, toppedUp := amount("total_balance"), amount("granted_balance"), amount("topped_up_balance")
	if remaining == nil && granted == nil && toppedUp == nil {
		return quota.Snapshot{}, fmt.Errorf("deepseek: %s missing usable total_balance, granted_balance and topped_up_balance; retry the balance request", url)
	}
	cur := "$"
	if code == "CNY" {
		cur = "¥"
	}
	var details []string
	if granted != nil {
		details = append(details, fmt.Sprintf("granted %s%.2f", cur, *granted))
	}
	if toppedUp != nil {
		details = append(details, fmt.Sprintf("topped-up %s%.2f", cur, *toppedUp))
	}
	return quota.Snapshot{Provider: "deepseek", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint,
		Credits: &quota.Balance{Remaining: remaining, Currency: cur, Detail: strings.Join(details, " · ")}}, nil
}
