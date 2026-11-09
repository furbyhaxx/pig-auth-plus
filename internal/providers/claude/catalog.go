package claude

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/catalog"
	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// ProviderID is the public provider id.
const ProviderID = "claude"

// wireID is the hidden provider in the private wire runtime.
const wireID = "auth-plus-claude-wire"

const defaultBaseURL = "https://api.anthropic.com"

const (
	// catalogURL is the account's own model listing.
	catalogURL = defaultBaseURL + "/v1/models?limit=1000"
	// pricesURL is models.dev's provider catalog; failing it keeps bundled prices.
	pricesURL        = "https://models.dev/api.json"
	catalogTTL       = 4 * time.Hour
	listingTimeout   = 15 * time.Second
	pricesTimeout    = 10 * time.Second
	anthropicVersion = "2023-06-01"
	oauthBeta        = "oauth-2025-04-20"
)

// bundledJSON is PiG's anthropic catalog at build time (`just catalog`). It serves the first
// run, the offline fallback and the compatibility metadata of models the account listing adds.
//
//go:embed catalog.json
var bundledJSON []byte

// Catalog holds the claude models: the account listing priced from models.dev, starting from
// the bundled snapshot.
type Catalog struct {
	mu sync.Mutex
	// base is the bundled snapshot; the listing copies compatibility metadata from it.
	base   []map[string]any
	models []map[string]any
}

// NewCatalog starts from the bundled snapshot.
func NewCatalog() (*Catalog, error) {
	var models []map[string]any
	if err := json.Unmarshal(bundledJSON, &models); err != nil {
		return nil, err
	}
	base := retag(models)
	return &Catalog{base: base, models: cloneModels(base)}, nil
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
	return cloneModels(c.models)
}

// unpricedKey marks a model the listing reported without any price; PiG's model store keeps it
// with the model, so an API-key session restores the same exclusion.
const unpricedKey = "claudeUnpriced"

// replace installs a refreshed listing.
func (c *Catalog) replace(models []map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = models
}

// Fetch is the refresh's transport: the account listing for the session credential and the
// price catalog. Tests replace the fields.
type Fetch struct {
	Listing func(context.Context, *sdk.Credential) ([]byte, error)
	Prices  func(context.Context) ([]byte, error)
}

// live is the production transport.
var live = Fetch{Listing: fetchListing, Prices: fetchPrices}

// Refresh restores the saved listing, refetches after the TTL and persists the result.
func (c *Catalog) Refresh(r sdk.RefreshModelsContext, fetch Fetch, now time.Time) error {
	credential := r.Credential
	token, oauth := credentialToken(credential)
	f := catalog.Refresh{
		Provider: ProviderID, TTL: catalogTTL, Now: now,
		Apply: func(l catalog.List) { c.replace(l.Models) },
	}
	if token != "" {
		f.Fetch = func(ctx context.Context) (catalog.List, error) {
			listing, err := fetch.Listing(ctx, credential)
			if err != nil {
				return catalog.List{}, err
			}
			prices, err := fetch.Prices(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return catalog.List{}, ctx.Err()
				}
				prices = nil
			}
			models, err := c.parse(listing, prices, oauth)
			if err != nil {
				return catalog.List{}, err
			}
			if len(models) == 0 {
				return catalog.List{}, &CatalogError{Endpoint: catalogURL, Reason: "no usable models in the listing"}
			}
			return catalog.List{Models: models}, nil
		}
	}
	return f.Run(r)
}

// credentialToken is the credential value the listing authenticates with, and whether it is a
// subscription.
func credentialToken(cred *sdk.Credential) (string, bool) {
	if cred.String("type") == "oauth" {
		return strings.TrimSpace(cred.String("access")), true
	}
	return strings.TrimSpace(cred.String("key")), false
}

// CatalogError is a failed account model listing; it never carries the response body or a token.
type CatalogError struct {
	Endpoint string
	Status   int
	Reason   string
}

func (e *CatalogError) Error() string {
	s := fmt.Sprintf("%s: models listing at %s", ProviderID, e.Endpoint)
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	return s + "; retry the model refresh or /login claude"
}

// listingError drops the provider body httpx keeps on a status error.
func listingError(err error) error {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return &CatalogError{Endpoint: catalogURL, Status: se.Status}
	}
	return err
}

func fetchListing(ctx context.Context, cred *sdk.Credential) ([]byte, error) {
	token, oauth := credentialToken(cred)
	header := http.Header{
		"Accept":            {"application/json"},
		"anthropic-version": {anthropicVersion},
		"User-Agent":        {UserAgent(PinnedVersion)},
	}
	if oauth {
		header.Set("Authorization", "Bearer "+token)
		header.Set("anthropic-beta", oauthBeta)
	} else {
		header.Set("x-api-key", token)
	}
	_, data, err := httpx.Do(ctx, httpx.Request{
		Provider: ProviderID, URL: catalogURL, Header: header, Timeout: listingTimeout, Secrets: []string{token},
	})
	if err != nil {
		return nil, listingError(err)
	}
	return data, nil
}

func fetchPrices(ctx context.Context) ([]byte, error) {
	_, data, err := httpx.Do(ctx, httpx.Request{
		Provider: ProviderID, URL: pricesURL, Timeout: pricesTimeout,
		Header: http.Header{"Accept": {"application/json"}},
	})
	return data, err
}

type supportFlag struct {
	Supported bool `json:"supported"`
}

type listingRow struct {
	ID             string      `json:"id"`
	DisplayName    string      `json:"display_name"`
	Line           string      `json:"line"`
	Lifecycle      string      `json:"lifecycle"`
	MaxInputTokens json.Number `json:"max_input_tokens"`
	MaxTokens      json.Number `json:"max_tokens"`
	Capabilities   *struct {
		ImageInput supportFlag `json:"image_input"`
		Thinking   *struct {
			Types *struct {
				Adaptive supportFlag `json:"adaptive"`
				Enabled  supportFlag `json:"enabled"`
				Disabled supportFlag `json:"disabled"`
			} `json:"types"`
		} `json:"thinking"`
		Effort *struct {
			Low    supportFlag `json:"low"`
			Medium supportFlag `json:"medium"`
			High   supportFlag `json:"high"`
			Xhigh  supportFlag `json:"xhigh"`
			Max    supportFlag `json:"max"`
		} `json:"effort"`
	} `json:"capabilities"`
}

type priceDoc struct {
	Anthropic struct {
		Models map[string]struct {
			Cost *struct {
				Input      *float64 `json:"input"`
				Output     *float64 `json:"output"`
				CacheRead  *float64 `json:"cache_read"`
				CacheWrite *float64 `json:"cache_write"`
			} `json:"cost"`
		} `json:"models"`
	} `json:"anthropic"`
}

// parse maps the account listing to PiG models. A listed id the bundled catalog knows keeps
// its bundled metadata; an unknown id copies non-price metadata from the newest bundled model
// of its line. Prices come from models.dev, then from the bundled model of the same id, and
// never from that template. Models without a price carry [unpricedKey] and are offered only to
// subscriptions.
func (c *Catalog) parse(listing, prices []byte, includeUnpriced bool) ([]map[string]any, error) {
	var envelope struct {
		Data    []json.RawMessage `json:"data"`
		HasMore *bool             `json:"has_more"`
	}
	if err := json.Unmarshal(listing, &envelope); err != nil {
		return nil, &CatalogError{Endpoint: catalogURL, Reason: "invalid model listing"}
	}
	if envelope.Data == nil || envelope.HasMore == nil || *envelope.HasMore {
		return nil, &CatalogError{Endpoint: catalogURL, Reason: "expected a complete model listing"}
	}
	table := parsePrices(prices)
	models := []map[string]any{}
	for _, raw := range envelope.Data {
		var row listingRow
		if json.Unmarshal(raw, &row) != nil || row.Lifecycle != "active" {
			continue
		}
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		known := c.known(id)
		cost := table.price(id)
		if cost == nil && known != nil {
			bundledCost, _ := known["cost"].(map[string]any)
			cost = maps.Clone(bundledCost)
		}
		if cost == nil && !includeUnpriced {
			continue
		}
		contextWindow, okContext := positive(row.MaxInputTokens)
		maxTokens, okMax := positive(row.MaxTokens)
		if known != nil {
			model := maps.Clone(known)
			model["id"] = id
			if cost != nil {
				model["cost"] = cost
			}
			if okContext {
				model["contextWindow"] = float64(contextWindow)
			}
			if okMax {
				model["maxTokens"] = float64(maxTokens)
			}
			models = append(models, model)
			continue
		}
		name := strings.TrimSpace(row.DisplayName)
		template := c.newest(row.Line)
		if template == nil || name == "" || !okContext || !okMax || row.Capabilities == nil ||
			row.Capabilities.Thinking == nil || row.Capabilities.Thinking.Types == nil || row.Capabilities.Effort == nil {
			continue
		}
		types, effort := row.Capabilities.Thinking.Types, row.Capabilities.Effort
		adaptive, enabled, disabled := types.Adaptive.Supported, types.Enabled.Supported, types.Disabled.Supported
		levels := map[string]any{"off": nil, "minimal": nil}
		if disabled {
			levels["off"] = "off"
		}
		levels["low"], levels["medium"], levels["high"] = levelOf("low", effort.Low), levelOf("medium", effort.Medium), levelOf("high", effort.High)
		levels["xhigh"], levels["max"] = levelOf("xhigh", effort.Xhigh), levelOf("max", effort.Max)
		input := []any{"text"}
		if row.Capabilities.ImageInput.Supported {
			input = append(input, "image")
		}
		model := maps.Clone(template)
		model["id"] = id
		model["name"] = name
		model["contextWindow"] = float64(contextWindow)
		model["maxTokens"] = float64(maxTokens)
		model["input"] = input
		model["reasoning"] = adaptive || enabled
		model["thinkingLevelMap"] = levels
		model["compat"] = compatOf(template, adaptive && !enabled && !disabled)
		if cost == nil {
			cost = zeroCost()
			model[unpricedKey] = true
		}
		model["cost"] = cost
		models = append(models, model)
	}
	return models, nil
}

// known is the bundled model of id, exact or without its date suffix.
func (c *Catalog) known(id string) map[string]any {
	for _, want := range []string{id, undated(id)} {
		for _, m := range c.base {
			if m["id"] == want {
				return m
			}
		}
	}
	return nil
}

// newest is the bundled model of line with the highest id, for example claude-fable-5-1 over
// claude-fable-5.
func (c *Catalog) newest(line string) map[string]any {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	prefix := "claude-" + line + "-"
	var best map[string]any
	for _, m := range c.base {
		id, _ := m["id"].(string)
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if best == nil || compareIDs(undated(id), undated(best["id"].(string))) > 0 {
			best = m
		}
	}
	return best
}

// compatOf keeps the template's non-price compatibility, dropping the fallbacks it names: they
// belong to the template's model, not to the listed one.
func compatOf(template map[string]any, adaptive bool) map[string]any {
	out := map[string]any{}
	compat, _ := template["compat"].(map[string]any)
	for k, v := range compat {
		if k == "allowedFallbackModels" {
			continue
		}
		out[k] = v
	}
	out["forceAdaptiveThinking"] = adaptive
	return out
}

// priceTable is models.dev's anthropic cost per model id; a row without all four prices is
// absent, and tiered prices are not read.
type priceTable map[string]map[string]any

func parsePrices(data []byte) priceTable {
	var doc priceDoc
	if len(data) == 0 || json.Unmarshal(data, &doc) != nil {
		return nil
	}
	table := priceTable{}
	for id, entry := range doc.Anthropic.Models {
		cost := entry.Cost
		if cost == nil || cost.Input == nil || cost.Output == nil || cost.CacheRead == nil || cost.CacheWrite == nil {
			continue
		}
		if *cost.Input < 0 || *cost.Output < 0 || *cost.CacheRead < 0 || *cost.CacheWrite < 0 {
			continue
		}
		table[id] = map[string]any{"input": *cost.Input, "output": *cost.Output, "cacheRead": *cost.CacheRead, "cacheWrite": *cost.CacheWrite}
	}
	return table
}

// price is the price of id, exact or without its date suffix.
func (t priceTable) price(id string) map[string]any {
	if cost, ok := t[id]; ok {
		return maps.Clone(cost)
	}
	if cost, ok := t[undated(id)]; ok {
		return maps.Clone(cost)
	}
	return nil
}

func positive(n json.Number) (int, bool) {
	v, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || v <= 0 || v > 1<<31-1 {
		return 0, false
	}
	return int(v), true
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// undated is id without a trailing release date, which the listing and models.dev may differ on.
func undated(id string) string { return dateSuffix.ReplaceAllString(id, "") }

// levelOf is the thinking level name a model reports as supported, or nil.
func levelOf(level string, flag supportFlag) any {
	if flag.Supported {
		return level
	}
	return nil
}

// compareIDs compares two ids segment by segment, comparing digit runs numerically.
func compareIDs(a, b string) int {
	for a != "" && b != "" {
		x, restA := segment(a)
		y, restB := segment(b)
		if c := compareSegment(x, y); c != 0 {
			return c
		}
		a, b = restA, restB
	}
	return len(a) - len(b)
}

// compareSegment compares two segments numerically when both are digit runs.
func compareSegment(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return na - nb
	}
	return strings.Compare(a, b)
}

// segment splits s into its first run of digits or of non-digits.
func segment(s string) (head, rest string) {
	digits := s[0] >= '0' && s[0] <= '9'
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digits {
		i++
	}
	return s[:i], s[i:]
}

// zeroCost is the price of a model only a subscription may use.
func zeroCost() map[string]any {
	return map[string]any{"input": float64(0), "output": float64(0), "cacheRead": float64(0), "cacheWrite": float64(0)}
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
		c["cost"] = zeroCost()
		out[i] = c
	}
	return out
}

func cloneModels(models []map[string]any) []map[string]any {
	out := make([]map[string]any, len(models))
	for i, m := range models {
		out[i] = maps.Clone(m)
	}
	return out
}
