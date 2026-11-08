package claude

import (
	_ "embed"
	"encoding/json"
	"maps"
	"reflect"
	"sync"
)

// ProviderID is the public provider id.
const ProviderID = "claude"

// wireID is the hidden provider in the private wire runtime.
const wireID = "auth-plus-claude-wire"

const defaultBaseURL = "https://api.anthropic.com"

// bundled is PiG's anthropic catalog at build time (`just catalog`); it serves the first run
// until a session publishes the running host's catalog.
//
//go:embed catalog.json
var bundled []byte

// Catalog holds the claude models: PiG's anthropic catalog re-tagged to `claude`.
type Catalog struct {
	mu     sync.Mutex
	models []map[string]any
}

// NewCatalog starts from the bundled snapshot.
func NewCatalog() (*Catalog, error) {
	var models []map[string]any
	if err := json.Unmarshal(bundled, &models); err != nil {
		return nil, err
	}
	return &Catalog{models: retag(models)}, nil
}

func retag(anthropic []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(anthropic))
	for _, m := range anthropic {
		c := maps.Clone(m)
		c["provider"] = ProviderID
		c["api"] = "anthropic-messages"
		out = append(out, c)
	}
	return out
}

// Models returns a copy of the current catalog.
func (c *Catalog) Models() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.models))
	for i, m := range c.models {
		out[i] = maps.Clone(m)
	}
	return out
}

// Update replaces the catalog from the host's anthropic models and reports a change.
func (c *Catalog) Update(anthropic []map[string]any) bool {
	if len(anthropic) == 0 {
		return false
	}
	next := retag(anthropic)
	c.mu.Lock()
	defer c.mu.Unlock()
	if reflect.DeepEqual(normalize(next), normalize(c.models)) {
		return false
	}
	c.models = next
	return true
}

// normalize compares through JSON so number types from different decoders agree.
func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// wireConfig is the hidden provider configuration for the current catalog.
func (c *Catalog) wireConfig() map[string]any {
	models := c.Models()
	base := defaultBaseURL
	for _, m := range models {
		if s, ok := m["baseUrl"].(string); ok && s != "" {
			base = s
			break
		}
	}
	for _, m := range models {
		delete(m, "provider")
		delete(m, "api")
	}
	return map[string]any{"api": "anthropic-messages", "baseUrl": base, "apiKey": "unused", "models": models}
}

// freeOf zeroes cost: subscription turns are prepaid.
func freeOf(models []map[string]any) []map[string]any {
	out := make([]map[string]any, len(models))
	for i, m := range models {
		c := maps.Clone(m)
		c["cost"] = map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}
		out[i] = c
	}
	return out
}
