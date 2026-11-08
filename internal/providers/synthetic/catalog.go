package synthetic

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// ProviderID is the public provider id.
const ProviderID = "synthetic"

// wireID is the hidden provider in the private wire runtime.
const wireID = "auth-plus-synthetic-wire"

const (
	baseURL    = "https://api.synthetic.new/anthropic"
	catalogURL = "https://api.synthetic.new/v1/models"
	// catalogTTL bounds how often sessions fetch the public catalog.
	catalogTTL     = 4 * time.Hour
	catalogTimeout = 15 * time.Second
)

// bundled is a captured `/v1/models` response that serves the first run.
//
//go:embed catalog.json
var bundled []byte

// hfRepo is an explicit Hugging Face model id: `hf:<org>/<repo>`.
var hfRepo = regexp.MustCompile(`^hf:([^/\s]+/[^/\s]+)$`)

// Catalog holds the Synthetic models and the tokenizer repository behind each id.
type Catalog struct {
	mu     sync.Mutex
	models []map[string]any
	repos  map[string]string
}

// NewCatalog starts from the bundled snapshot.
func NewCatalog() (*Catalog, error) {
	models, repos, err := ParseCatalog(bundled)
	if err != nil {
		return nil, fmt.Errorf("bundled catalog: %w", err)
	}
	return &Catalog{models: models, repos: repos}, nil
}

// Models returns a copy of the current catalog.
func (c *Catalog) Models() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneModels(c.models)
}

// Repo is the Hugging Face repository whose tokenizer the model id uses, or "".
func (c *Catalog) Repo(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if repo := c.repos[id]; repo != "" {
		return repo
	}
	if m := hfRepo.FindStringSubmatch(id); m != nil {
		return m[1]
	}
	return ""
}

// replace installs models, keeping known repositories for ids the new list lacks a mapping for.
func (c *Catalog) replace(models []map[string]any, repos map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = models
	merged := maps.Clone(c.repos)
	maps.Copy(merged, repos)
	c.repos = merged
}

// Fetch downloads and parses the live catalog.
func Fetch(ctx context.Context) ([]map[string]any, map[string]string, error) {
	_, data, err := httpx.Do(ctx, httpx.Request{
		Provider: ProviderID, URL: catalogURL, Timeout: catalogTimeout,
		Header: map[string][]string{"Accept": {"application/json"}},
	})
	if err != nil {
		return nil, nil, err
	}
	models, repos, err := ParseCatalog(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: invalid catalog from %s: %w; retry the model refresh", ProviderID, catalogURL, err)
	}
	return models, repos, nil
}

type catalogRow struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	HuggingFaceID   string          `json:"hugging_face_id"`
	AlwaysOn        bool            `json:"always_on"`
	ContextLength   json.Number     `json:"context_length"`
	MaxOutputLength json.Number     `json:"max_output_length"`
	Features        json.RawMessage `json:"supported_features"`
	Modalities      []string        `json:"input_modalities"`
	Pricing         map[string]any  `json:"pricing"`
}

// ParseCatalog maps a `/v1/models` response to PiG models. It rejects a malformed envelope and
// skips rows that are not always on, lack limits or prices, or do not support tools.
func ParseCatalog(data []byte) ([]map[string]any, map[string]string, error) {
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, nil, err
	}
	if envelope.Data == nil {
		return nil, nil, errors.New("expected a data array")
	}
	models := []map[string]any{}
	repos := map[string]string{}
	for _, raw := range envelope.Data {
		var row catalogRow
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		id := strings.TrimSpace(row.ID)
		contextWindow, okC := positive(row.ContextLength)
		maxTokens, okM := positive(row.MaxOutputLength)
		if id == "" || !row.AlwaysOn || !okC || !okM {
			continue
		}
		var features []string
		if len(row.Features) > 0 && json.Unmarshal(row.Features, &features) != nil {
			continue
		}
		if row.Features != nil && !contains(features, "tools") {
			continue
		}
		input, okI := price(row.Pricing["prompt"])
		output, okO := price(row.Pricing["completion"])
		if !okI || !okO {
			continue
		}
		cacheRead, _ := price(row.Pricing["input_cache_reads"])
		cacheWrite, _ := price(row.Pricing["input_cache_writes"])
		inputs := []any{"text"}
		if contains(row.Modalities, "image") {
			inputs = append(inputs, "image")
		}
		name := firstNonBlank(row.Name, row.HuggingFaceID, id)
		models = append(models, map[string]any{
			"id": id, "name": name, "provider": ProviderID, "api": "anthropic-messages", "baseUrl": baseURL,
			"reasoning": contains(features, "reasoning"), "input": inputs,
			"contextWindow": contextWindow, "maxTokens": maxTokens,
			"cost": map[string]any{"input": input, "output": output, "cacheRead": cacheRead, "cacheWrite": cacheWrite},
		})
		if repo := strings.TrimSpace(row.HuggingFaceID); repo != "" && strings.Count(repo, "/") == 1 {
			repos[id] = repo
		}
	}
	return models, repos, nil
}

// Refresh restores the stored catalog, then fetches a new one when the network is allowed and
// the stored one is missing, stale or a refresh is forced. A restored catalog replaces the
// bundled one rather than overlaying it, so retired models disappear.
func (c *Catalog) Refresh(r sdk.RefreshModelsContext, fetch func(context.Context) ([]map[string]any, map[string]string, error), now time.Time) error {
	ctx := r.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	restored := parseStored(r.Stored)
	if restored != nil {
		published, err := r.Publish(sdk.ModelsPublication{Update: func() error { c.replace(restored, nil); return nil }})
		if err != nil || !published {
			return err
		}
	}
	if !r.AllowNetwork || ctx.Err() != nil {
		return nil
	}
	force := r.Force != nil && *r.Force
	if restored != nil && !force && storedFresh(r.Stored, now) {
		return nil
	}
	models, repos, err := fetch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	persist, err := json.Marshal(map[string]any{"models": models, "checkedAt": now.UnixMilli()})
	if err != nil {
		return err
	}
	_, err = r.Publish(sdk.ModelsPublication{Persist: persist, Update: func() error { c.replace(models, repos); return nil }})
	return err
}

// parseStored keeps only stored models this provider can serve; nil means none usable.
func parseStored(stored map[string]any) []map[string]any {
	list, _ := stored["models"].([]any)
	out := []map[string]any{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok || m["provider"] != ProviderID {
			continue
		}
		if id, _ := m["id"].(string); strings.TrimSpace(id) == "" {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// storedFresh reports whether the stored catalog was checked within the TTL.
func storedFresh(stored map[string]any, now time.Time) bool {
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
	age := now.Sub(time.UnixMilli(int64(at)))
	return age >= 0 && age < catalogTTL
}

func positive(n json.Number) (int, bool) {
	v, err := n.Int64()
	if err != nil || v <= 0 || v > math.MaxInt32 {
		return 0, false
	}
	return int(v), true
}

var number = regexp.MustCompile(`(?i)-?\d+(?:\.\d+)?(?:e[+-]?\d+)?`)

// price is dollars per million tokens; the API mixes per-token ("$0.0000006") and per-million
// strings. Zero is a real price; a missing or negative value is not.
func price(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	match := number.FindString(s)
	if match == "" {
		return 0, false
	}
	amount, err := strconv.ParseFloat(match, 64)
	if err != nil || amount < 0 || math.IsInf(amount, 0) {
		return 0, false
	}
	if amount < 0.001 {
		amount *= 1_000_000
	}
	return amount, true
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cloneModels(models []map[string]any) []map[string]any {
	out := make([]map[string]any, len(models))
	for i, m := range models {
		out[i] = maps.Clone(m)
	}
	return out
}
