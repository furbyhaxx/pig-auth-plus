package wire

import (
	"context"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestRelayRewritesOnlyTheDoneMessage(t *testing.T) {
	inner := sdk.CreateAssistantMessageEventStream()
	out := Relay(context.Background(), inner, Label{}, func(m map[string]any) map[string]any {
		return map[string]any{"role": "assistant", "rewritten": true, "durationMs": 1}
	})
	inner.Push(map[string]any{"type": "text_delta", "delta": "x"})
	inner.Push(map[string]any{"type": "done", "reason": "stop", "message": map[string]any{"role": "assistant", "durationMs": 1}})
	var types []string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for e := range out.Events(ctx) {
		types = append(types, e["type"].(string))
	}
	if len(types) != 2 || types[1] != "done" {
		t.Fatalf("events = %v", types)
	}
	if out.Result()["rewritten"] != true {
		t.Fatalf("result = %v", out.Result())
	}
}

func TestRelayPassesErrorsThrough(t *testing.T) {
	inner := sdk.CreateAssistantMessageEventStream()
	out := Relay(context.Background(), inner, Label{}, func(map[string]any) map[string]any {
		t.Fatal("finalize called for an error")
		return nil
	})
	inner.Push(map[string]any{"type": "error", "reason": "error", "error": map[string]any{"role": "assistant", "stopReason": "error", "durationMs": 1}})
	if out.Result()["stopReason"] != "error" {
		t.Fatalf("result = %v", out.Result())
	}
}

func TestRelayRelabelsWireMessages(t *testing.T) {
	inner := sdk.CreateAssistantMessageEventStream()
	out := Relay(context.Background(), inner, Label{Wire: "w", Provider: "claude", API: "anthropic-messages"}, nil)
	inner.Push(map[string]any{"type": "start", "partial": map[string]any{"role": "assistant", "provider": "w"}})
	inner.Push(map[string]any{"type": "done", "reason": "stop", "message": map[string]any{"role": "assistant", "provider": "w", "api": "x", "durationMs": 1}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for e := range out.Events(ctx) {
		m, _ := e["partial"].(map[string]any)
		if m == nil {
			m, _ = e["message"].(map[string]any)
		}
		if m["provider"] != "claude" {
			t.Fatalf("%v not relabeled: %v", e["type"], m)
		}
	}
	if r := out.Result(); r["provider"] != "claude" || r["api"] != "anthropic-messages" {
		t.Fatalf("result = %v", r)
	}
}
