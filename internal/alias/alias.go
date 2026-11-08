// Package alias turns configured model aliases into PiG virtual models: an alias is listed
// under its source's provider and every request is routed to the source model.
package alias

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

// Resolved is a valid alias with the values it was registered with.
type Resolved struct {
	Provider, ID, Source, Name string
	ContextWindow, MaxTokens   int
	Levels                     []string
	Input                      []string
}

// Plan resolves aliases against the host's catalog. Valid aliases keep their configured order;
// each rejected alias yields one problem. physical must hold only models the host lists
// itself, never aliases from an earlier plan.
func Plan(aliases []config.Alias, physical []map[string]any) (valid []Resolved, problems []string) {
	byKey := map[string]map[string]any{}
	for _, m := range physical {
		byKey[str(m["provider"])+"/"+str(m["id"])] = m
	}
	seen := map[string]bool{}
	for _, a := range aliases {
		key := a.Provider + "/" + a.ID
		switch {
		case a.Provider == "" || a.ID == "" || a.Source == "":
			problems = append(problems, fmt.Sprintf("alias %q needs a provider, an id and a source", key))
			continue
		case a.ID == a.Source:
			problems = append(problems, fmt.Sprintf("alias %s is its own source", key))
			continue
		case seen[key]:
			problems = append(problems, fmt.Sprintf("alias %s is defined twice; the first one wins", key))
			continue
		case byKey[key] != nil:
			problems = append(problems, fmt.Sprintf("alias %s collides with a model of that provider", key))
			continue
		}
		src := byKey[a.Provider+"/"+a.Source]
		if src == nil {
			problems = append(problems, fmt.Sprintf("alias %s: source model %s/%s is not in the catalog", key, a.Provider, a.Source))
			continue
		}
		seen[key] = true
		r := Resolved{
			Provider: a.Provider, ID: a.ID, Source: a.Source,
			Name:          a.Name,
			ContextWindow: int(orDefault(a.ContextWindow, num(src["contextWindow"]))),
			MaxTokens:     int(orDefault(a.MaxTokens, num(src["maxTokens"]))),
			Levels:        sdk.GetSupportedThinkingLevels(src),
		}
		if r.Name == "" {
			r.Name = str(src["name"])
			if r.Name == "" {
				r.Name = a.Source
			}
			r.Name += " (" + a.ID + ")"
		}
		if in, ok := src["input"].([]any); ok {
			for _, v := range in {
				r.Input = append(r.Input, str(v))
			}
		}
		valid = append(valid, r)
	}
	return valid, problems
}

func orDefault(v int64, d int64) int64 {
	if v > 0 {
		return v
	}
	return d
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case interface{ Int64() (int64, error) }:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// Host is what a sync needs from the extension host.
type Host interface {
	Register(sdk.VirtualModel) error
	Unregister(provider, id string)
	// Physical lists the models the host lists itself, without any alias this package registered.
	Physical() ([]map[string]any, error)
	// Find returns the physical model a request is routed to.
	Find(provider, id string) (map[string]any, error)
}

// Registry keeps the registered aliases in step with the configuration. It is safe for
// concurrent use.
type Registry struct {
	host Host

	mu         sync.Mutex
	registered map[string]Resolved
}

// NewRegistry creates an empty registry over host.
func NewRegistry(host Host) *Registry {
	return &Registry{host: host, registered: map[string]Resolved{}}
}

// Registered lists the aliases currently registered, ordered by provider and id.
func (r *Registry) Registered() []Resolved {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Resolved, 0, len(r.registered))
	for _, v := range r.registered {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b Resolved) int { return strings.Compare(a.Provider+"/"+a.ID, b.Provider+"/"+b.ID) })
	return out
}

// Sync registers new and changed aliases and unregisters removed ones, and returns one problem
// per rejected alias. An unreadable catalog changes nothing and is an error.
func (r *Registry) Sync(aliases []config.Alias) (problems []string, err error) {
	all, err := r.host.Physical()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	physical := all[:0:0]
	for _, m := range all {
		if _, ours := r.registered[str(m["provider"])+"/"+str(m["id"])]; !ours {
			physical = append(physical, m)
		}
	}
	valid, problems := Plan(aliases, physical)
	want := map[string]Resolved{}
	for _, v := range valid {
		want[v.Provider+"/"+v.ID] = v
	}
	for key, old := range r.registered {
		if _, keep := want[key]; !keep {
			r.host.Unregister(old.Provider, old.ID)
			delete(r.registered, key)
		}
	}
	for _, v := range valid {
		key := v.Provider + "/" + v.ID
		if old, ok := r.registered[key]; ok && slices.Equal(old.Levels, v.Levels) && old.Source == v.Source &&
			old.Name == v.Name && old.ContextWindow == v.ContextWindow && old.MaxTokens == v.MaxTokens && slices.Equal(old.Input, v.Input) {
			continue
		}
		v := v
		err := r.host.Register(sdk.VirtualModel{
			Provider: v.Provider, ID: v.ID, Name: v.Name, ThinkingLevels: v.Levels,
			ContextWindow: v.ContextWindow, MaxTokens: v.MaxTokens, Input: v.Input,
			Route: r.route(v),
		})
		if err != nil {
			problems = append(problems, fmt.Sprintf("alias %s: %v", key, err))
			continue
		}
		r.registered[key] = v
	}
	return problems, nil
}

// route sends every request to the source model with the thinking level the user selected.
func (r *Registry) route(v Resolved) sdk.ModelRouteFunc {
	return func(_ sdk.Context, req sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
		src, err := r.host.Find(v.Provider, v.Source)
		if err != nil {
			return sdk.ModelRoute{}, fmt.Errorf("alias %s/%s: %w", v.Provider, v.ID, err)
		}
		if src == nil {
			return sdk.ModelRoute{}, fmt.Errorf("alias %s/%s: source model %s is no longer in the catalog", v.Provider, v.ID, v.Source)
		}
		return sdk.ModelRoute{Model: src, ThinkingLevel: sdk.ClampThinkingLevel(src, req.ThinkingLevel)}, nil
	}
}

// Close unregisters everything this registry registered.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, v := range r.registered {
		r.host.Unregister(v.Provider, v.ID)
		delete(r.registered, key)
	}
}
