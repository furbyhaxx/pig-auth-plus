package claude

import (
	"context"
	"maps"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// Provider wires the claude catalog, auth and request shaping to a native PiG provider.
type Provider struct {
	Catalog *Catalog
	Auth    *Auth
	Shaper  *Shaper
	Runtime *wire.Runtime
}

// Native builds the native provider registration.
func (p *Provider) Native() *sdk.Provider {
	base := defaultBaseURL
	return &sdk.Provider{
		ID: ProviderID, Name: "Claude", BaseURL: &base,
		Auth:         p.Auth.ProviderAuth(),
		GetModels:    func() ([]map[string]any, error) { return p.Catalog.Models(), nil },
		FilterModels: p.filterModels,
		RefreshModels: func(r sdk.RefreshModelsContext) error {
			return p.Catalog.Refresh(r, live, time.Now())
		},
		Stream:       p.streamer(false),
		StreamSimple: p.streamer(true),
	}
}

// filterModels applies the credential: subscription turns are prepaid, and a model without a
// verified price is not offered to an API key.
func (p *Provider) filterModels(models []map[string]any, cred *sdk.Credential) ([]map[string]any, error) {
	if t, _ := cred.Get("type"); t == "oauth" {
		return freeOf(models), nil
	}
	priced := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if unpriced, _ := m[unpricedKey].(bool); unpriced {
			continue
		}
		priced = append(priced, m)
	}
	return priced, nil
}

func (p *Provider) streamer(simple bool) sdk.ProviderStreamFunc {
	return func(model, request map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		return p.stream(simple, model, request, opts)
	}
}

func (p *Provider) stream(simple bool, model, request map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	signal := opts.Signal
	if signal == nil {
		signal = context.Background()
	}
	options := maps.Clone(opts.Values)
	client, exchange := p.Shaper.Exchange(options, signal)
	if headers, ok := options["headers"].(map[string]any); ok {
		headers = maps.Clone(headers)
		delete(headers, AccountHeader)
		options["headers"] = headers
	}
	options["fetch"] = wire.Fetch(client, exchange)
	if opts.OnPayload != nil {
		options["onPayload"] = sdk.OnPayloadFunc(func(payload any, _ map[string]any) (any, error) {
			return opts.OnPayload(payload, model)
		})
	}
	if opts.OnResponse != nil {
		options["onResponse"] = sdk.OnResponseFunc(func(response, _ map[string]any) error {
			return opts.OnResponse(response, model)
		})
	}
	return p.Runtime.Stream(wire.Provider{Name: wireID, Config: p.Catalog.wireConfig()}, wire.Call{Simple: simple, Model: model, Request: request, Options: options, Signal: signal})
}
