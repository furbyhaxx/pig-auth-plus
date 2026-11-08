package hooks

import (
	"reflect"
	"testing"
)

var anthropic = Model{Provider: "claude", ID: "m", API: "anthropic-messages"}

func cc(ttl string) map[string]any {
	c := map[string]any{"type": "ephemeral"}
	if ttl != "" {
		c["ttl"] = ttl
	}
	return map[string]any{"type": "text", "text": "x", "cache_control": c}
}

func TestCacheRetentionMovesAllBreakpointsTogether(t *testing.T) {
	payload := map[string]any{"model": "m",
		"system":   []any{cc("")},
		"tools":    []any{cc("")},
		"messages": []any{map[string]any{"role": "user", "content": []any{cc("")}}, map[string]any{"role": "user", "content": "plain"}},
	}
	got := CacheRetention(payload, anthropic, "long", []string{"claude"})
	if got == nil {
		t.Fatal("no rewrite")
	}
	ttl := func(v any) any { return v.([]any)[0].(map[string]any)["cache_control"].(map[string]any)["ttl"] }
	if ttl(got["system"]) != "1h" || ttl(got["tools"]) != "1h" ||
		ttl(got["messages"].([]any)[0].(map[string]any)["content"]) != "1h" {
		t.Fatalf("not all breakpoints are 1h: %v", got)
	}
	if _, has := payload["system"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)["ttl"]; has {
		t.Fatal("input payload was mutated")
	}
	back := CacheRetention(got, anthropic, "short", []string{"claude"})
	if ttl(back["system"]) != nil {
		t.Fatalf("short did not remove ttl: %v", back["system"])
	}
}

func TestCacheRetentionLeavesUnsuitableRequests(t *testing.T) {
	payload := map[string]any{"model": "m", "system": []any{cc("1h")}, "messages": []any{}}
	cases := map[string]func() map[string]any{
		"already long":   func() map[string]any { return CacheRetention(payload, anthropic, "long", []string{"claude"}) },
		"default":        func() map[string]any { return CacheRetention(payload, anthropic, "default", []string{"claude"}) },
		"other provider": func() map[string]any { return CacheRetention(payload, anthropic, "short", []string{"anthropic"}) },
		"helper model": func() map[string]any {
			return CacheRetention(map[string]any{"model": "x", "messages": []any{}}, anthropic, "short", []string{"claude"})
		},
		"no long support": func() map[string]any {
			m := anthropic
			m.Compat = map[string]any{"supportsLongCacheRetention": false}
			return CacheRetention(map[string]any{"model": "m", "system": []any{cc("")}, "messages": []any{}}, m, "long", []string{"claude"})
		},
	}
	for name, run := range cases {
		if run() != nil {
			t.Errorf("%s: payload was rewritten", name)
		}
	}
}

func TestVerbosityPrefersModelOverrideAndKeepsTextFormat(t *testing.T) {
	m := Model{Provider: "openai", ID: "gpt", API: "openai-responses", SamplingParams: map[string]any{"text": map[string]any{"verbosity": "low"}}}
	payload := map[string]any{"model": "gpt", "input": []any{}, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}
	got := Verbosity(payload, m, "high", []string{"openai"})
	want := map[string]any{"format": map[string]any{"type": "json_object"}, "verbosity": "low"}
	if got == nil || !reflect.DeepEqual(got["text"], want) {
		t.Fatalf("text = %v", got)
	}
	m.SamplingParams = nil
	if Verbosity(payload, m, "default", []string{"openai"}) != nil {
		t.Fatal("default must not rewrite")
	}
	m.API = "openai-completions"
	if Verbosity(payload, m, "high", []string{"openai"}) != nil {
		t.Fatal("non-Responses API must not be rewritten")
	}
}
