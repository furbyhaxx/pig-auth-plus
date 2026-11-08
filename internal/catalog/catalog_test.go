package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type publisher struct{ persisted []json.RawMessage }

func (p *publisher) publish(pub sdk.ModelsPublication) (bool, error) {
	if pub.Persist != nil {
		p.persisted = append(p.persisted, pub.Persist)
	}
	return true, pub.Update()
}

func TestRefreshHonorsTTLForceOfflineAndFailure(t *testing.T) {
	now := time.UnixMilli(10_000_000_000)
	stored := map[string]any{"checkedAt": float64(now.Add(-time.Hour).UnixMilli()), "extra": map[string]any{"k": "v"}, "models": []any{
		map[string]any{"id": "kept", "provider": "p"},
		map[string]any{"id": "foreign", "provider": "other"},
	}}
	var current List
	fetches := 0
	r := Refresh{Provider: "p", TTL: 4 * time.Hour, Now: now, Apply: func(l List) { current = l },
		Fetch: func(context.Context) (List, error) {
			fetches++
			return List{Models: []map[string]any{{"id": "fresh", "provider": "p"}}, Extra: map[string]any{"k": "new"}}, nil
		}}
	p := &publisher{}
	if err := r.Run(sdk.RefreshModelsContext{Stored: stored, AllowNetwork: true, Publish: p.publish}); err != nil {
		t.Fatal(err)
	}
	if fetches != 0 || len(current.Models) != 1 || current.Models[0]["id"] != "kept" || current.Extra["k"] != "v" {
		t.Fatalf("fresh stored: fetches=%d current=%v", fetches, current)
	}
	force := true
	if err := r.Run(sdk.RefreshModelsContext{Stored: stored, AllowNetwork: true, Force: &force, Publish: p.publish}); err != nil {
		t.Fatal(err)
	}
	if fetches != 1 || current.Models[0]["id"] != "fresh" || len(p.persisted) != 1 {
		t.Fatalf("forced: fetches=%d current=%v", fetches, current)
	}
	var persisted map[string]any
	_ = json.Unmarshal(p.persisted[0], &persisted)
	if persisted["extra"].(map[string]any)["k"] != "new" || persisted["checkedAt"] != float64(now.UnixMilli()) {
		t.Fatalf("persisted = %v", persisted)
	}
	stale := map[string]any{"checkedAt": float64(now.Add(-5 * time.Hour).UnixMilli()), "models": stored["models"]}
	if err := r.Run(sdk.RefreshModelsContext{Stored: stale, Publish: p.publish}); err != nil || fetches != 1 {
		t.Fatalf("offline fetched: %v %d", err, fetches)
	}
	r.Fetch = func(context.Context) (List, error) { return List{}, errors.New("down") }
	if err := r.Run(sdk.RefreshModelsContext{Stored: stale, AllowNetwork: true, Publish: p.publish}); err == nil {
		t.Fatal("failure not reported")
	}
	if current.Models[0]["id"] != "kept" {
		t.Fatalf("failed refresh must keep the restored catalog: %v", current)
	}
}
