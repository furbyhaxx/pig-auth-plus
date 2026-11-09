package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const testNow = 1_800_000_000_000

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

type publisher struct{ persisted []json.RawMessage }

func (p *publisher) publish(pub sdk.ModelsPublication) (bool, error) {
	if pub.Persist != nil {
		p.persisted = append(p.persisted, pub.Persist)
	}
	return true, pub.Update()
}

// listingFetch serves the captured listing and price catalog; either transport may fail.
func listingFetch(t *testing.T, pricesErr error) Fetch {
	listing := fixture(t, "v1-models.json")
	prices := fixture(t, "models-dev-anthropic.json")
	return Fetch{
		Listing: func(context.Context, *sdk.Credential) ([]byte, error) { return listing, nil },
		Prices: func(context.Context) ([]byte, error) {
			if pricesErr != nil {
				return nil, pricesErr
			}
			return prices, nil
		},
	}
}

func registered(t *testing.T, catalog *Catalog, credential *sdk.Credential) []map[string]any {
	t.Helper()
	native := (&Provider{Catalog: catalog, Auth: &Auth{Endpoints: DefaultEndpoints}}).Native()
	models, err := native.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	models, err = native.FilterModels(models, credential)
	if err != nil {
		t.Fatal(err)
	}
	return models
}

func model(models []map[string]any, id string) map[string]any {
	for _, m := range models {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func costOf(t *testing.T, m map[string]any) [4]float64 {
	t.Helper()
	cost, ok := m["cost"].(map[string]any)
	if !ok {
		t.Fatalf("model %v has no cost", m["id"])
	}
	var out [4]float64
	for i, key := range [4]string{"input", "output", "cacheRead", "cacheWrite"} {
		value, ok := cost[key].(float64)
		if !ok {
			t.Fatalf("cost %s = %v", key, cost[key])
		}
		out[i] = value
	}
	return out
}

func TestRefreshPricesNewModelForAPIKey(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	pub := &publisher{}
	credential := sdk.NewCredential("type", "api_key", "key", "test-key")
	r := sdk.RefreshModelsContext{AllowNetwork: true, Credential: credential, Signal: context.Background(), Publish: pub.publish}
	if err := catalog.Refresh(r, listingFetch(t, nil), time.UnixMilli(testNow)); err != nil {
		t.Fatal(err)
	}
	models := registered(t, catalog, credential)
	fable := model(models, "claude-fable-5-2")
	if fable == nil {
		t.Fatalf("claude-fable-5-2 missing: %v", models)
	}
	if cost := costOf(t, fable); cost != [4]float64{8, 40, 0.2, 10} {
		t.Fatalf("claude-fable-5-2 cost = %v", cost)
	}
	if fable["name"] != "Claude Fable 5.2" || fable["contextWindow"] != float64(1000000) || fable["maxTokens"] != float64(128000) {
		t.Fatalf("claude-fable-5-2 = %v", fable)
	}
	if fable["reasoning"] != true || len(fable["input"].([]any)) != 2 {
		t.Fatalf("claude-fable-5-2 capabilities = %v", fable)
	}
	if levels, _ := fable["thinkingLevelMap"].(map[string]any); levels["max"] != "max" || levels["low"] != "low" || levels["off"] != nil {
		t.Fatalf("claude-fable-5-2 thinkingLevelMap = %v", fable["thinkingLevelMap"])
	}
	if compat, _ := fable["compat"].(map[string]any); compat["forceAdaptiveThinking"] != true || compat["allowedFallbackModels"] != nil {
		t.Fatalf("claude-fable-5-2 compat = %v", fable["compat"])
	}
	if cost := costOf(t, model(models, "claude-fable-5-1")); cost != [4]float64{10, 50, 0.25, 12.5} {
		t.Fatalf("claude-fable-5-1 cost = %v", cost)
	}
	if model(models, "claude-sonnet-4-5-20250929") != nil {
		t.Fatal("a listing row that is no longer active was offered")
	}
	var stored map[string]any
	if err := json.Unmarshal(pub.persisted[0], &stored); err != nil {
		t.Fatal(err)
	}
	if len(pub.persisted) != 1 || stored["checkedAt"] != float64(testNow) {
		t.Fatalf("persisted = %v", stored)
	}
}

func TestRefreshWithoutPricesKeepsBundledCostsAndHidesUnpricedFromAPIKeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		credential *sdk.Credential
		listed     bool
	}{
		{"subscription", sdk.NewCredential("type", "oauth", "access", "test-access"), true},
		{"api key", sdk.NewCredential("type", "api_key", "key", "test-key"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := NewCatalog()
			if err != nil {
				t.Fatal(err)
			}
			pub := &publisher{}
			r := sdk.RefreshModelsContext{AllowNetwork: true, Credential: tc.credential, Signal: context.Background(), Publish: pub.publish}
			if err := catalog.Refresh(r, listingFetch(t, errors.New("models.dev down")), time.UnixMilli(testNow)); err != nil {
				t.Fatalf("a price-source failure must not fail the refresh: %v", err)
			}
			models := registered(t, catalog, tc.credential)
			if listed := model(models, "claude-fable-5-2") != nil; listed != tc.listed {
				t.Fatalf("claude-fable-5-2 listed = %v, want %v", listed, tc.listed)
			}
			if cost := costOf(t, model(catalog.Models(), "claude-fable-5-1")); cost != [4]float64{10, 50, 0.25, 12.5} {
				t.Fatalf("claude-fable-5-1 cost = %v", cost)
			}
			var stored struct {
				Models []map[string]any `json:"models"`
			}
			if err := json.Unmarshal(pub.persisted[0], &stored); err != nil {
				t.Fatal(err)
			}
			if marked := model(stored.Models, "claude-fable-5-2")[unpricedKey]; marked != true && tc.listed {
				t.Fatalf("the stored model does not keep the unpriced marker: %v", marked)
			}
		})
	}
}

func TestRefreshFailureKeepsThePreviousCatalog(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	pub := &publisher{}
	credential := sdk.NewCredential("type", "oauth", "access", "test-access")
	r := sdk.RefreshModelsContext{AllowNetwork: true, Credential: credential, Signal: context.Background(), Publish: pub.publish}
	if err := catalog.Refresh(r, listingFetch(t, nil), time.UnixMilli(testNow)); err != nil {
		t.Fatal(err)
	}
	before := catalog.Models()
	failing := Fetch{
		Listing: func(context.Context, *sdk.Credential) ([]byte, error) {
			return nil, &CatalogError{Endpoint: catalogURL, Status: 503}
		},
		Prices: func(context.Context) ([]byte, error) { return fixture(t, "models-dev-anthropic.json"), nil },
	}
	force := true
	r.Force = &force
	err = catalog.Refresh(r, failing, time.UnixMilli(testNow))
	if err == nil || err.Error() != "claude: models listing at "+catalogURL+" (HTTP 503); retry the model refresh or /login claude" {
		t.Fatalf("err = %v", err)
	}
	if after := catalog.Models(); !equalModels(before, after) {
		t.Fatalf("failed refresh changed the catalog: %d -> %d models", len(before), len(after))
	}
}

func TestParseDatedIDsUseUndatedPricesAndUnknownLinesAreSkipped(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	listing := []byte(`{"data":[
		{"id":"claude-fable-5-1-20261109","display_name":"ignored","line":"fable","lifecycle":"active","max_input_tokens":100,"max_tokens":10},
		{"id":"claude-fable-6","display_name":"Claude Fable 6","line":"fable","lifecycle":"active","max_input_tokens":1000000,"max_tokens":128000,
		 "capabilities":{"image_input":{"supported":true},"thinking":{"types":{"adaptive":{"supported":true}}},"effort":{"low":{"supported":true}}}},
		{"id":"claude-unknown-1","display_name":"Unknown","line":"unknown","lifecycle":"active","max_input_tokens":100,"max_tokens":10}
	],"has_more":false}`)
	prices := []byte(`{"anthropic":{"models":{"claude-fable-5-1":{"cost":{"input":10,"output":50,"cache_read":0.25,"cache_write":12.5,
		"tiers":[{"input":99,"output":99}]}}}}}`)
	models, err := catalog.parse(listing, prices, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2: %v", len(models), models)
	}
	if models[0]["id"] != "claude-fable-5-1-20261109" || models[0]["name"] != "Claude Fable 5.1" ||
		models[0]["contextWindow"] != float64(100) || models[0]["maxTokens"] != float64(10) {
		t.Fatalf("dated id = %v", models[0])
	}
	if cost := costOf(t, models[0]); cost != [4]float64{10, 50, 0.25, 12.5} {
		t.Fatalf("dated id cost = %v", cost)
	}
	if cost := costOf(t, models[1]); cost != [4]float64{} {
		t.Fatalf("unpriced template model cost = %v", cost)
	}
	if models[1][unpricedKey] != true {
		t.Fatalf("claude-fable-6 is not marked unpriced: %v", models[1])
	}
}

func TestParseRejectsIncompleteListing(t *testing.T) {
	catalog, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, listing := range []string{`{"data":[],"has_more":true}`, `{"models":[]}`, `not json`} {
		if _, err := catalog.parse([]byte(listing), nil, true); err == nil {
			t.Fatalf("accepted %s", listing)
		}
	}
}

func equalModels(a, b []map[string]any) bool {
	first, _ := json.Marshal(a)
	second, _ := json.Marshal(b)
	return string(first) == string(second)
}
