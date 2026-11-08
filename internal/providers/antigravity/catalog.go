package antigravity

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/catalog"
	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// catalogTTL matches the CLI's model-config reload; entitlements can change the list.
const catalogTTL = 5 * time.Minute

//go:embed catalog.json
var bundled []byte

var (
	effortSuffix = regexp.MustCompile(`-(minimal|low|medium|high)$`)
	nameSuffix   = regexp.MustCompile(`(?i) \((minimal|low|medium|high)\)$`)
)

// Catalog holds the current Antigravity models.
type Catalog struct {
	mu     sync.Mutex
	models []map[string]any
}

// NewCatalog starts from the bundled snapshot.
func NewCatalog() (*Catalog, error) {
	models, err := ParseCatalog(bundled)
	if err != nil {
		return nil, fmt.Errorf("bundled catalog: %w", err)
	}
	return &Catalog{models: models}, nil
}

// Models returns a deep copy of the current catalog.
func (c *Catalog) Models() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, _ := json.Marshal(c.models)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// LevelMap returns the stored thinkingLevelMap of a model id.
func (c *Catalog) LevelMap(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.models {
		if m["id"] == id {
			lm, _ := m["thinkingLevelMap"].(map[string]any)
			return lm
		}
	}
	return nil
}

func (c *Catalog) replace(models []map[string]any) {
	c.mu.Lock()
	c.models = models
	c.mu.Unlock()
}

// Refresh restores, refetches after the TTL with the stored OAuth login, and persists.
func (c *Catalog) Refresh(r sdk.RefreshModelsContext, fetch func(context.Context, string) ([]map[string]any, error), now time.Time) error {
	access := ""
	if r.Credential != nil && r.Credential.String("type") == "oauth" {
		access = r.Credential.String("access")
	}
	f := catalog.Refresh{Provider: ID, TTL: catalogTTL, Now: now, Apply: func(l catalog.List) { c.replace(l.Models) }}
	if access != "" {
		f.Fetch = func(ctx context.Context) (catalog.List, error) {
			models, err := fetch(ctx, access)
			return catalog.List{Models: models}, err
		}
	}
	return f.Run(r)
}

// CatalogError is a failed catalog refresh; it never carries tokens or bodies.
type CatalogError struct {
	Reason string
	Status int
}

func (e *CatalogError) Error() string {
	s := "antigravity: fetchAvailableModels"
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	return s + ": " + e.Reason + "; retry the model refresh or /login antigravity"
}

// Fetcher downloads the agent catalog for the session's project.
type Fetcher struct {
	BaseURL  string
	Projects *Projects
}

// Fetch resolves the project and parses fetchAvailableModels.
func (f Fetcher) Fetch(ctx context.Context, access string) ([]map[string]any, error) {
	project, err := f.Projects.Resolve(ctx, access)
	if err != nil {
		return nil, err
	}
	base := f.BaseURL
	if base == "" {
		base = BaseURL
	}
	body, _ := json.Marshal(map[string]string{"project": project})
	_, data, err := httpx.Do(ctx, httpx.Request{
		Provider: ID, Method: http.MethodPost, URL: base + "/v1internal:fetchAvailableModels",
		Header: CLIHeaders(access), Body: body, Timeout: 15 * time.Second, Secrets: []string{access, project},
	})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) {
			return nil, &CatalogError{Reason: "request failed", Status: se.Status}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &CatalogError{Reason: "request failed"}
	}
	return ParseCatalog(data)
}

type catalogRow struct {
	DisplayName    string `json:"displayName"`
	Model          string `json:"model"`
	MaxTokens      int    `json:"maxTokens"`
	MaxOutput      int    `json:"maxOutputTokens"`
	SupportsImages bool   `json:"supportsImages"`
	ThinkingLevel  *int   `json:"thinkingLevel"`
	ThinkingBudget *int   `json:"thinkingBudget"`
	Adaptive       bool   `json:"supportsAdaptiveThinking"`
}

// ParseCatalog maps the agent sort's low/medium/high entries into one model per family whose
// thinkingLevelMap carries each level. A malformed envelope fails; unusable rows are skipped.
func ParseCatalog(data []byte) ([]map[string]any, error) {
	var env struct {
		Models map[string]json.RawMessage `json:"models"`
		Sorts  []struct {
			Groups []struct {
				ModelIDs []string `json:"modelIds"`
			} `json:"groups"`
		} `json:"agentModelSorts"`
	}
	if err := json.Unmarshal(data, &env); err != nil || env.Models == nil || env.Sorts == nil {
		return nil, &CatalogError{Reason: "expected models and agentModelSorts"}
	}
	var order []string
	families := map[string]map[string]any{}
	for _, sort := range env.Sorts {
		for _, group := range sort.Groups {
			for _, id := range group.ModelIDs {
				m := effortSuffix.FindStringSubmatch(id)
				if m == nil || m[1] == "minimal" {
					continue
				}
				var row catalogRow
				if raw, ok := env.Models[id]; !ok || json.Unmarshal(raw, &row) != nil {
					continue
				}
				thinking, ok := row.thinking()
				if strings.TrimSpace(row.DisplayName) == "" || strings.TrimSpace(row.Model) == "" || row.MaxTokens <= 0 || row.MaxOutput <= 0 || !ok {
					continue
				}
				family := strings.TrimSuffix(id, m[0])
				model, seen := families[family]
				if !seen {
					model = map[string]any{
						"id": family, "name": nameSuffix.ReplaceAllString(row.DisplayName, ""),
						"provider": ID, "api": API, "baseUrl": BaseURL, "reasoning": true,
						"input":         []any{"text"},
						"contextWindow": row.MaxTokens, "maxTokens": row.MaxOutput,
						"cost":             map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
						"thinkingLevelMap": map[string]any{"off": nil, "minimal": nil, "low": nil, "medium": nil, "high": nil},
					}
					families[family] = model
					order = append(order, family)
				}
				model["contextWindow"] = min(model["contextWindow"].(int), row.MaxTokens)
				model["maxTokens"] = min(model["maxTokens"].(int), row.MaxOutput)
				if row.SupportsImages {
					model["input"] = []any{"text", "image"}
				}
				model["thinkingLevelMap"].(map[string]any)[m[1]] = Level{ID: id, Enum: row.Model, Thinking: thinking}.Encode()
			}
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		out = append(out, families[id])
	}
	return out, nil
}

func (r catalogRow) thinking() (Thinking, bool) {
	if r.ThinkingLevel != nil {
		effort := map[int]string{1: "LOW", 2: "MEDIUM", 3: "HIGH"}[*r.ThinkingLevel]
		if effort != "" {
			kind := "level"
			if r.Adaptive {
				kind = "adaptive"
			}
			return Thinking{Kind: kind, Effort: effort}, true
		}
	}
	if r.ThinkingBudget != nil && *r.ThinkingBudget >= -1 {
		return Thinking{Kind: "budget", Budget: *r.ThinkingBudget}, true
	}
	return Thinking{}, false
}
