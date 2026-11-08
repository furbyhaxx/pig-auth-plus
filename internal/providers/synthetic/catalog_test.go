package synthetic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestParseCatalogFiltersAndPrices(t *testing.T) {
	data := []byte(`{"data":[
		{"id":"hf:a/b","always_on":true,"hugging_face_id":"a/b","context_length":1000,"max_output_length":100,
		 "supported_features":["tools","reasoning"],"input_modalities":["text","image"],
		 "pricing":{"prompt":"$0.0000006","completion":"2.5","input_cache_reads":"0"}},
		{"id":"off","always_on":false,"context_length":1000,"max_output_length":100,"pricing":{"prompt":"0","completion":"0"}},
		{"id":"notools","always_on":true,"context_length":1000,"max_output_length":100,"supported_features":["reasoning"],"pricing":{"prompt":"0","completion":"0"}},
		{"id":"noprice","always_on":true,"context_length":1000,"max_output_length":100,"pricing":{"prompt":"-1","completion":"0"}},
		{"id":"nolimit","always_on":true,"context_length":0,"max_output_length":100,"pricing":{"prompt":"0","completion":"0"}},
		{"id":"syn:x","always_on":true,"hugging_face_id":"c/d","context_length":10,"max_output_length":5,"pricing":{"prompt":"0","completion":"0"}}
	]}`)
	models, repos, err := ParseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2: %v", len(models), models)
	}
	m := models[0]
	cost := m["cost"].(map[string]any)
	if cost["input"].(float64) < 0.5999 || cost["input"].(float64) > 0.6001 || cost["output"] != 2.5 {
		t.Fatalf("cost = %v", cost)
	}
	if m["reasoning"] != true || len(m["input"].([]any)) != 2 {
		t.Fatalf("model = %v", m)
	}
	if models[1]["id"] != "syn:x" || repos["syn:x"] != "c/d" {
		t.Fatalf("alias repo lost: %v %v", models[1], repos)
	}
	if _, _, err := ParseCatalog([]byte(`{"models":[]}`)); err == nil {
		t.Fatal("missing data array accepted")
	}
}

func TestBundledCatalogParses(t *testing.T) {
	c, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models()) == 0 {
		t.Fatal("bundled catalog is empty")
	}
	if c.Repo("syn:large:text") == "" || c.Repo("hf:x/y") != "x/y" || c.Repo("unknown") != "" {
		t.Fatalf("repos: %q %q %q", c.Repo("syn:large:text"), c.Repo("hf:x/y"), c.Repo("unknown"))
	}
}

type publisher struct{ persisted []json.RawMessage }

func (p *publisher) publish(pub sdk.ModelsPublication) (bool, error) {
	if pub.Persist != nil {
		p.persisted = append(p.persisted, pub.Persist)
	}
	return true, pub.Update()
}

func TestRefreshHonorsTTLAndReplaces(t *testing.T) {
	now := time.UnixMilli(10_000_000_000)
	stored := map[string]any{"checkedAt": float64(now.Add(-time.Hour).UnixMilli()), "models": []any{
		map[string]any{"id": "kept", "provider": ProviderID},
		map[string]any{"id": "foreign", "provider": "other"},
	}}
	fetches := 0
	fetch := func(context.Context) ([]map[string]any, map[string]string, error) {
		fetches++
		return []map[string]any{{"id": "fresh", "provider": ProviderID}}, map[string]string{"fresh": "o/r"}, nil
	}
	c, _ := NewCatalog()
	p := &publisher{}
	if err := c.Refresh(sdk.RefreshModelsContext{Stored: stored, AllowNetwork: true, Publish: p.publish}, fetch, now); err != nil {
		t.Fatal(err)
	}
	if fetches != 0 || len(c.Models()) != 1 || c.Models()[0]["id"] != "kept" {
		t.Fatalf("fresh stored catalog: fetches=%d models=%v", fetches, c.Models())
	}
	force := true
	if err := c.Refresh(sdk.RefreshModelsContext{Stored: stored, AllowNetwork: true, Force: &force, Publish: p.publish}, fetch, now); err != nil {
		t.Fatal(err)
	}
	if fetches != 1 || c.Models()[0]["id"] != "fresh" || c.Repo("fresh") != "o/r" || len(p.persisted) != 1 {
		t.Fatalf("forced refresh: fetches=%d models=%v persisted=%d", fetches, c.Models(), len(p.persisted))
	}
	stale := map[string]any{"checkedAt": float64(now.Add(-5 * time.Hour).UnixMilli()), "models": stored["models"]}
	if err := c.Refresh(sdk.RefreshModelsContext{Stored: stale, AllowNetwork: false, Publish: p.publish}, fetch, now); err != nil || fetches != 1 {
		t.Fatalf("offline refresh fetched: %v %d", err, fetches)
	}
	failing := func(context.Context) ([]map[string]any, map[string]string, error) {
		return nil, nil, errors.New("down")
	}
	if err := c.Refresh(sdk.RefreshModelsContext{Stored: stale, AllowNetwork: true, Publish: p.publish}, failing, now); err == nil {
		t.Fatal("fetch failure not reported")
	}
	if c.Models()[0]["id"] != "kept" {
		t.Fatalf("failed refresh must keep the restored catalog: %v", c.Models())
	}
}
