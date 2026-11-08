package synthetic

import (
	"context"
	"maps"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/apikey"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// Provider wires the Synthetic catalog, API key and usage correction to a native PiG provider.
type Provider struct {
	Catalog *Catalog
	Runtime *wire.Runtime
	// Count converts a reported prompt to real tokens; nil keeps Synthetic's units.
	Count PromptCounter
}

// Native builds the native provider registration.
func (p *Provider) Native() *sdk.Provider {
	base := baseURL
	return &sdk.Provider{
		ID: ProviderID, Name: "Synthetic", BaseURL: &base,
		Auth:      sdk.ProviderAuth{APIKey: apikey.Auth("Synthetic API key", "SYNTHETIC_API_KEY", "Enter Synthetic API key:")},
		GetModels: func() ([]map[string]any, error) { return p.Catalog.Models(), nil },
		RefreshModels: func(r sdk.RefreshModelsContext) error {
			return p.Catalog.Refresh(r, Fetch, time.Now())
		},
		Stream:       p.streamer(false),
		StreamSimple: p.streamer(true),
	}
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
	var (
		mu      sync.Mutex
		payload []byte
	)
	options["fetch"] = wire.Fetch(wire.Client, wire.Exchange{
		Signal: signal,
		Request: func(r *wire.Request) error {
			mu.Lock()
			payload = r.Body
			mu.Unlock()
			return nil
		},
	})
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
	id, _ := model["id"].(string)
	return p.Runtime.Stream(wire.Provider{Name: wireID, Config: p.wireConfig()}, wire.Call{
		Simple: simple, Model: model, Request: request, Options: options, Signal: signal,
		Finalize: func(message map[string]any) map[string]any {
			mu.Lock()
			body := payload
			mu.Unlock()
			rates, _ := model["cost"].(map[string]any)
			return finalize(message, id, rates, body, p.Count)
		},
	})
}

// wireConfig is the hidden provider configuration for the current catalog.
func (p *Provider) wireConfig() map[string]any {
	models := p.Catalog.Models()
	for _, m := range models {
		delete(m, "provider")
		delete(m, "api")
	}
	return map[string]any{"api": "anthropic-messages", "baseUrl": baseURL, "apiKey": "unused", "models": models}
}
