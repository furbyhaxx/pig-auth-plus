// Package wire streams pig-auth-plus providers through PiG's builtin wire protocols.
//
// A public provider's streamSimple hands its request to a hidden provider in one private
// ModelRuntime, whose builtin API serializes the request and parses the response, while a
// request-local fetch shapes the HTTP exchange. See ANA-provider-delegation.
package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Provider is one hidden wire provider: a builtin API, base URL and the public provider's models.
type Provider struct {
	// Name must differ from every public provider id.
	Name string
	// Config is a host provider configuration without functions.
	Config map[string]any
}

// Runtime lazily owns the private ModelRuntime and the wire providers registered in it.
type Runtime struct {
	dir func() (string, error)

	mu         sync.Mutex
	host       *sdk.Context
	rt         *sdk.ModelRuntime
	registered map[string]string
}

// NewRuntime keeps the private runtime's auth and model store under the directory dir resolves;
// it is resolved on first use because the host's facts are unavailable in the factory.
func NewRuntime(dir func() (string, error)) *Runtime {
	return &Runtime{dir: dir}
}

// Bind attaches the session's retained Context; native provider streams receive none. A nil
// Context unbinds at session end. Either way the next stream creates a new runtime.
func (r *Runtime) Bind(c *sdk.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host, r.rt, r.registered = c, nil, nil
}

// Call is one public-provider stream routed through a wire provider.
type Call struct {
	Simple bool
	// Model and Request are the public provider's; the model keeps its id.
	Model, Request map[string]any
	// Options carry the credential and the fetch callback.
	Options map[string]any
	// Signal ends the relay; the fetch exchange cancels the inner stream with it.
	Signal context.Context
	// Finalize may rewrite the final assistant message; nil keeps it.
	Finalize func(message map[string]any) map[string]any
}

// Stream streams c through the wire provider p. History and results carry the public
// provider's identity; only the wire runtime sees p's name.
func (r *Runtime) Stream(p Provider, c Call) (*sdk.ModelEventStream, error) {
	rt, err := r.ensure(p)
	if err != nil {
		return nil, err
	}
	public, _ := c.Model["provider"].(string)
	publicAPI, _ := c.Model["api"].(string)
	request := r.Ordered(c.Request)
	m := maps.Clone(c.Model)
	m["provider"] = p.Name
	delete(m, "api")
	request = adoptHistory(request, public, p.Name, p.Config["api"])
	var inner *sdk.ModelEventStream
	if c.Simple {
		inner = rt.StreamSimple(m, request, c.Options)
	} else {
		inner = rt.Stream(m, request, c.Options)
	}
	return Relay(c.Signal, inner, Label{Wire: p.Name, Provider: public, API: publicAPI}, c.Finalize), nil
}

// adoptHistory relabels the public provider's assistant messages as the wire provider's. PiG
// keeps thinking blocks and signatures only for messages of the same provider, API and model;
// without this every earlier turn would lose them, breaking thinking tool loops and cache prefixes.
func adoptHistory(request map[string]any, public, wireName string, wireAPI any) map[string]any {
	messages, ok := request["messages"].([]any)
	if !ok || public == "" {
		return request
	}
	var adopted []any
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "assistant" || msg["provider"] != public {
			continue
		}
		if adopted == nil {
			adopted = slices.Clone(messages)
		}
		relabeled := maps.Clone(msg)
		relabeled["provider"] = wireName
		if wireAPI != nil {
			relabeled["api"] = wireAPI
		}
		adopted[i] = relabeled
	}
	if adopted == nil {
		return request
	}
	out := maps.Clone(request)
	out["messages"] = adopted
	return out
}

// Ordered restores the system prompt section order of request from the session's composed
// prompt; without a session, sections keep name order.
func (r *Runtime) Ordered(request map[string]any) map[string]any {
	r.mu.Lock()
	host := r.host
	r.mu.Unlock()
	prompt := ""
	if host != nil {
		prompt, _ = host.GetSystemPrompt()
	}
	return OrderSections(request, prompt)
}

func (r *Runtime) ensure(p Provider) (*sdk.ModelRuntime, error) {
	fingerprint, err := json.Marshal(p.Config)
	if err != nil {
		return nil, fmt.Errorf("wire provider %s: %w", p.Name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.host == nil {
		return nil, errors.New("no active session")
	}
	if r.rt == nil {
		rt, err := r.create(*r.host)
		if err != nil {
			return nil, err
		}
		r.rt, r.registered = rt, map[string]string{}
	}
	if r.registered[p.Name] != string(fingerprint) {
		if err := r.rt.RegisterProvider(p.Name, p.Config); err != nil {
			return nil, fmt.Errorf("wire provider %s: %w", p.Name, err)
		}
		r.registered[p.Name] = string(fingerprint)
	}
	return r.rt, nil
}

// create isolates the runtime from the user's credentials, models.json and model store.
func (r *Runtime) create(c sdk.Context) (*sdk.ModelRuntime, error) {
	dir, err := r.dir()
	if err != nil {
		return nil, fmt.Errorf("wire runtime: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wire runtime: %w", err)
	}
	refresh := false
	rt, err := sdk.ModelRuntimeCreate(c, sdk.ModelRuntimeCreateOptions{
		AuthPath:        filepath.Join(dir, "auth.json"),
		NoModelsFile:    true,
		ModelsStorePath: filepath.Join(dir, "models-store.json"),
		RefreshOnCreate: &refresh,
	})
	if err != nil {
		return nil, fmt.Errorf("wire runtime: %w", err)
	}
	return rt, nil
}
