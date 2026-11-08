// Package catalog implements the refresh policy shared by providers with a dynamic model list:
// restore the persisted list, refetch after a TTL, persist what was fetched.
package catalog

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// List is one catalog: the models and provider-specific data persisted with them.
type List struct {
	Models []map[string]any `json:"models"`
	Extra  map[string]any   `json:"extra,omitempty"`
}

// Refresh describes one provider's catalog refresh.
type Refresh struct {
	Provider string
	TTL      time.Duration
	Now      time.Time
	// Fetch downloads the live list; nil skips fetching (for example without a credential).
	Fetch func(context.Context) (List, error)
	// Apply installs a list as the provider's current catalog.
	Apply func(List)
}

// Run restores the stored list, then fetches when the network is allowed and the stored list
// is missing, older than TTL or a refresh is forced. A fetch canceled by the host is no error.
func (f Refresh) Run(r sdk.RefreshModelsContext) error {
	ctx := r.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	restored, ok := f.stored(r.Stored)
	if ok {
		published, err := r.Publish(sdk.ModelsPublication{Update: func() error { f.Apply(restored); return nil }})
		if err != nil || !published {
			return err
		}
	}
	if !r.AllowNetwork || f.Fetch == nil || ctx.Err() != nil {
		return nil
	}
	if ok && (r.Force == nil || !*r.Force) && f.fresh(r.Stored) {
		return nil
	}
	list, err := f.Fetch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	persist, err := json.Marshal(map[string]any{"models": list.Models, "extra": list.Extra, "checkedAt": f.Now.UnixMilli()})
	if err != nil {
		return err
	}
	_, err = r.Publish(sdk.ModelsPublication{Persist: persist, Update: func() error { f.Apply(list); return nil }})
	return err
}

// stored keeps only this provider's models with an id; no usable model means no stored list.
func (f Refresh) stored(stored map[string]any) (List, bool) {
	raw, _ := stored["models"].([]any)
	var list List
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok || m["provider"] != f.Provider {
			continue
		}
		if id, _ := m["id"].(string); id == "" {
			continue
		}
		list.Models = append(list.Models, m)
	}
	list.Extra, _ = stored["extra"].(map[string]any)
	return list, len(list.Models) > 0
}

func (f Refresh) fresh(stored map[string]any) bool {
	var at float64
	switch v := stored["checkedAt"].(type) {
	case float64:
		at = v
	case json.Number:
		at, _ = v.Float64()
	case int64:
		at = float64(v)
	default:
		return false
	}
	age := f.Now.Sub(time.UnixMilli(int64(at)))
	return age >= 0 && age < f.TTL
}
