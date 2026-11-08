package app

import (
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/hooks"
)

// onProviderRequest applies the verbosity and cache retention settings to requests of the
// selected model. Cache warming replays carry the same breakpoints as the turn they replay,
// so retention follows them. PiG leaves the purpose empty outside its cache-warming stream
// path, so an empty purpose counts as a turn; the payload's model id still has to match.
func (a *App) onProviderRequest(ctx sdk.Context, data map[string]any) (any, error) {
	purpose, _ := data["purpose"].(string)
	if purpose != "" && purpose != "turn" && purpose != "cache_warm" {
		return nil, nil
	}
	provider, id := ctx.ModelProvider(), ctx.Model()
	if provider == "" || id == "" {
		return nil, nil
	}
	s := a.settings()
	wantVerbosity := purpose != "cache_warm" && s.Verbosity != "default" && slices.Contains(s.VerbosityProviders, provider)
	wantRetention := s.CacheRetention != "default" && slices.Contains(s.CacheRetentionProviders, provider)
	if !wantVerbosity && !wantRetention {
		return nil, nil
	}
	found, err := ctx.ModelRegistry().Find(provider, id)
	if err != nil || found == nil {
		return nil, nil
	}
	m := hooks.Model{Provider: provider, ID: id}
	m.API, _ = found["api"].(string)
	m.SamplingParams, _ = found["samplingParams"].(map[string]any)
	m.Compat, _ = found["compat"].(map[string]any)
	payload := data["payload"]
	var out any
	if wantVerbosity {
		if next := hooks.Verbosity(payload, m, s.Verbosity, s.VerbosityProviders); next != nil {
			payload, out = next, next
		}
	}
	if wantRetention {
		if next := hooks.CacheRetention(payload, m, s.CacheRetention, s.CacheRetentionProviders); next != nil {
			out = next
		}
	}
	return out, nil
}
