package wire

import "testing"

func TestAdoptHistoryRelabelsOnlyThePublicProvidersAssistantTurns(t *testing.T) {
	own := map[string]any{"role": "assistant", "provider": "claude", "api": "anthropic-messages", "model": "m"}
	other := map[string]any{"role": "assistant", "provider": "openai", "api": "openai-responses", "model": "x"}
	user := map[string]any{"role": "user", "provider": "claude"}
	request := map[string]any{"messages": []any{user, own, other}}
	out := adoptHistory(request, "claude", "wire-claude", "anthropic-messages")
	got := out["messages"].([]any)
	if got[1].(map[string]any)["provider"] != "wire-claude" || got[1].(map[string]any)["api"] != "anthropic-messages" {
		t.Fatalf("own turn = %v", got[1])
	}
	if got[0].(map[string]any)["provider"] != "claude" || got[2].(map[string]any)["provider"] != "openai" {
		t.Fatalf("other messages changed: %v", got)
	}
	if own["provider"] != "claude" || request["messages"].([]any)[1].(map[string]any)["provider"] != "claude" {
		t.Fatal("caller's request was mutated")
	}
}
