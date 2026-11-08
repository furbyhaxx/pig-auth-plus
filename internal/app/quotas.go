package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota/sources"
)

// requestRefreshAge is how stale the active provider may be before a finished turn refreshes it.
const requestRefreshAge = 2 * time.Second

func newRegistry(a *App, ps *providers, p paths.Paths) *quota.Registry {
	return quota.NewRegistry(
		sources.Claude(),
		sources.Antigravity(ps.antigravity.Projects),
		sources.Synthetic(),
		sources.Codex(sources.CodexFileAt(p.CodexLegacyAuth())),
		sources.XAI(),
		sources.OpenRouter(),
		sources.DeepSeek(),
	)
}

func offline(env func(string) string) bool {
	for _, k := range []string{"PIG_OFFLINE", "PI_OFFLINE"} {
		switch strings.ToLower(env(k)) {
		case "1", "true", "yes":
			return true
		}
	}
	return false
}

// startQuotas builds and starts the session's engine.
func (a *App) startQuotas(st *state) error {
	ps := a.providers
	e := quota.NewEngine(quota.Options{
		Registry:  newRegistry(a, ps, st.paths),
		CachePath: st.paths.QuotaCache(),
		Config: func() quota.Config {
			s := a.settings()
			return quota.Config{Providers: s.QuotaProviders, Interval: time.Duration(s.RefreshIntervalSec) * time.Second}
		},
		AuthFor: func(ctx context.Context, provider string) (*quota.Auth, error) {
			return providerAuth(st.host, provider)
		},
		Offline: func() bool { return offline(os.Getenv) },
	})
	st.quotas = e
	return e.Start(st.group.Context())
}

// providerAuth reads PiG's resolved credential, which includes OAuth refresh. A provider with no
// credential is signed out, not an error.
func providerAuth(ctx sdk.Context, provider string) (*quota.Auth, error) {
	reg := ctx.ModelRegistry()
	status, err := reg.GetProviderAuthStatus(provider)
	if err != nil {
		return nil, fmt.Errorf("check %s credential: %w", provider, err)
	}
	if !status.Configured {
		return nil, nil
	}
	result, err := reg.GetProviderAuth(provider)
	if err != nil {
		return nil, fmt.Errorf("resolve %s credential: %w", provider, err)
	}
	return authFrom(result), nil
}

// authFrom extracts the bearer token from an auth result, whether PiG resolved an API key or an
// Authorization header.
func authFrom(result map[string]any) *quota.Auth {
	auth, _ := result["auth"].(map[string]any)
	if key, _ := auth["apiKey"].(string); key != "" {
		return &quota.Auth{Token: key}
	}
	headers, _ := auth["headers"].(map[string]any)
	for name, v := range headers {
		if !strings.EqualFold(name, "authorization") {
			continue
		}
		if value, _ := v.(string); len(value) > 7 && strings.EqualFold(value[:7], "bearer ") {
			return &quota.Auth{Token: strings.TrimSpace(value[7:])}
		}
	}
	return nil
}

// onProviderResponse feeds a turn's response headers to the provider that served it.
func (a *App) onProviderResponse(ctx sdk.Context, data map[string]any) (any, error) {
	st := a.session()
	if st == nil || st.quotas == nil {
		return nil, nil
	}
	if purpose, _ := data["purpose"].(string); purpose != "" && purpose != "turn" && purpose != "cache_warm" {
		return nil, nil
	}
	raw, _ := data["headers"].(map[string]any)
	headers := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	// Strict: a header set is only trusted from the provider that served the request.
	st.quotas.IngestHeaders(st.group.Context(), headers, ctx.ModelProvider(), true)
	return nil, nil
}

// onTurnEnd refreshes the active provider once a turn finishes, still honoring its floor.
func (a *App) onTurnEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	st := a.session()
	if st == nil || st.quotas == nil || !a.settings().RefreshOnRequest {
		return nil, nil
	}
	src := st.quotas.Registry().Resolve(ctx.ModelProvider())
	if src == nil {
		return nil, nil
	}
	id := src.ID
	st.group.Go(func(gctx context.Context) {
		_ = st.quotas.Refresh(gctx, id, quota.RefreshOptions{MinAge: requestRefreshAge})
	})
	return nil, nil
}
