package synthetic

import (
	"context"
	"encoding/json"
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

func TestRefreshPersistsRepositoriesForRestoredAliases(t *testing.T) {
	now := time.UnixMilli(10_000_000_000)
	var persisted json.RawMessage
	publish := func(pub sdk.ModelsPublication) (bool, error) {
		if pub.Persist != nil {
			persisted = pub.Persist
		}
		return true, pub.Update()
	}
	fetch := func(context.Context) ([]map[string]any, map[string]string, error) {
		return []map[string]any{{"id": "syn:new", "provider": ProviderID}}, map[string]string{"syn:new": "o/r"}, nil
	}
	c, _ := NewCatalog()
	if err := c.Refresh(sdk.RefreshModelsContext{AllowNetwork: true, Publish: publish}, fetch, now); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	_ = json.Unmarshal(persisted, &stored)
	restarted, _ := NewCatalog()
	if err := restarted.Refresh(sdk.RefreshModelsContext{Stored: stored, Publish: publish}, nil, now); err != nil {
		t.Fatal(err)
	}
	if restarted.Repo("syn:new") != "o/r" {
		t.Fatalf("restored alias lost its repository: %q", restarted.Repo("syn:new"))
	}
}
