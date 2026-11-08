// Package wire streams pig-auth-plus providers through PiG's builtin wire protocols.
//
// A public provider's streamSimple hands its request to a hidden provider in one private
// ModelRuntime, whose builtin API serializes the request and parses the response, while a
// request-local fetch shapes the HTTP exchange. See ANA-provider-delegation.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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

// Stream streams model through the wire provider p, with Pi's streamSimple options when simple
// is set. The model keeps its id; options carry the credential and fetch callback.
func (r *Runtime) Stream(p Provider, simple bool, model, request, options map[string]any) (*sdk.ModelEventStream, error) {
	rt, err := r.ensure(p)
	if err != nil {
		return nil, err
	}
	m := maps.Clone(model)
	m["provider"] = p.Name
	delete(m, "api")
	if simple {
		return rt.StreamSimple(m, request, options), nil
	}
	return rt.Stream(m, request, options), nil
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
