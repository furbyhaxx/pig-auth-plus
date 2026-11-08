package sources

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// SyntheticQuotasURL is the quota endpoint.
const SyntheticQuotasURL = "https://api.synthetic.new/v2/quotas"

const syntheticRecovery = "check the Synthetic API key or /login synthetic"

// Synthetic reports the subscription request count, the weekly plan budget and hourly search
// requests.
func Synthetic() *quota.Source {
	return &quota.Source{
		ID: "synthetic", Label: "Synthetic", Short: "syn", ParserVersion: 1,
		Fetch: fetchSynthetic(SyntheticQuotasURL),
	}
}

func fetchSynthetic(url string) func(context.Context, quota.Env) (quota.Snapshot, error) {
	return func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
		auth, err := env.Auth(ctx)
		if err != nil {
			return quota.Snapshot{}, err
		}
		if auth == nil {
			return needsAuth("synthetic", env.Now(), "Synthetic quotas: credentials missing; "+syntheticRecovery), nil
		}
		body, err := getJSON(ctx, "synthetic", http.MethodGet, url, bearer(auth.Token), nil, auth.Token)
		if err != nil {
			return quota.Snapshot{}, err
		}
		return ParseSynthetic(body, env.Now())
	}
}

func requestWindow(meter map[string]any, w quota.Window) (quota.Window, bool) {
	if meter == nil {
		return w, false
	}
	w.ResetAtMs = timeMs(meter["renewsAt"])
	limit, requests := num(meter["limit"]), num(meter["requests"])
	if limit != nil && *limit > 0 && requests != nil {
		w.Used = frac(quota.Frac(*requests / *limit))
		w.Value = fmt.Sprintf("%g/%g used", *requests, *limit)
	}
	return w, true
}

// ParseSynthetic reads the payload without carrying account metadata into the snapshot. A meter
// with missing numbers stays visible as unknown.
func ParseSynthetic(body map[string]any, now time.Time) (quota.Snapshot, error) {
	sub, weekly := obj(body["subscription"]), obj(body["planWeeklyLimit"])
	hourly := obj(obj(body["search"])["hourly"])
	if sub == nil && weekly == nil && hourly == nil {
		return quota.Snapshot{}, fmt.Errorf("Synthetic quotas (GET %s): unrecognized quota payload: missing subscription, planWeeklyLimit and search.hourly objects; %s", SyntheticQuotasURL, syntheticRecovery)
	}
	var ws []quota.Window
	if w, ok := requestWindow(sub, quota.Window{ID: "subscription", Short: "sub", Label: "Subscription requests", Important: true}); ok {
		ws = append(ws, w)
	}
	if weekly != nil {
		w := quota.Window{ID: "7d", Short: "7d", Label: "7d plan budget", Period: "7d", Important: true, ResetAtMs: timeMs(weekly["nextRegenAt"])}
		if left := num(weekly["percentRemaining"]); left != nil {
			w.Used = frac(quota.Frac(1 - *left/100))
			w.Value = strconv.FormatFloat(math.Round(*left*100)/100, 'f', -1, 64) + "% left"
		}
		ws = append(ws, w)
	}
	if w, ok := requestWindow(hourly, quota.Window{ID: "search-1h", Short: "srch", Label: "1h search requests", Period: "1h", Scope: "search"}); ok {
		ws = append(ws, w)
	}
	return quota.Snapshot{Provider: "synthetic", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws}, nil
}
