package tokens

import (
	"context"
	"testing"
)

type fakeNative struct {
	EstimateCounter
	count Count
	ok    bool
	got   *NativeInput
}

func (f *fakeNative) Context(_ context.Context, in NativeInput) (Count, bool) {
	f.got = &in
	return f.count, f.ok
}

func engine(c Counter) *Engine { return &Engine{For: func(string, string) Counter { return c }} }

func conv(t *testing.T, parts ...string) []map[string]any {
	out := make([]map[string]any, len(parts))
	for i, p := range parts {
		out[i] = decode(t, p)
	}
	return out
}

func TestContextAnchorsOnUsageAndCountsOnlyTheTail(t *testing.T) {
	in := Input{
		Provider: "p", Model: "m", ContextWindow: 1000,
		Messages: conv(t, `{"role":"user","content":"q"}`, `{"role":"assistant","stopReason":"stop","usage":{"totalTokens":100},"content":[{"type":"text","text":"a"}]}`, `{"role":"user","content":"12345678"}`),
		Entries:  []ProjectedEntry{{"e1", 1}, {"e2", 1}, {"e3", 1}},
		Branch:   []BranchEntry{{"e1", "message"}, {"e2", "message"}, {"e3", "message"}},
	}
	r, ok := engine(EstimateCounter{DefaultRatios}).Context(context.Background(), in)
	if !ok || r.Anchor == nil || r.Anchor.Tokens != 100 || r.Tail.Tokens != 2 || r.Tokens.Tokens != 102 || r.TailMessages != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Tokens.Source != SourceEstimate || r.Anchor.Source != SourceProvider {
		t.Fatalf("sources %+v", r)
	}
	if r.Percent == nil || *r.Percent != 10.2 {
		t.Fatalf("percent %v", r.Percent)
	}
}

func TestContextWithoutAnchorCountsSystemToolsAndMessages(t *testing.T) {
	in := Input{
		Provider: "p", Model: "m", LiveSystem: "abcdefgh",
		LiveTools: []ToolSchema{{Name: "t", Description: "d", Parameters: map[string]any{}}},
		Messages:  conv(t, `{"role":"user","content":"abcd"}`),
	}
	r, ok := engine(EstimateCounter{DefaultRatios}).Context(context.Background(), in)
	if !ok || r.Anchor != nil || r.Tokens.Tokens <= 3 || r.Percent != nil {
		t.Fatalf("%+v", r)
	}
}

func TestContextUsesNativeCountWhenAvailableAndFallsBackWhenNot(t *testing.T) {
	in := Input{Provider: "p", Model: "m", LiveSystem: "sys", Messages: conv(t, `{"role":"user","content":"hello"}`)}
	f := &fakeNative{count: Count{Tokens: 42, Source: SourceEndpoint}, ok: true}
	r, _ := engine(f).Context(context.Background(), in)
	if r.Tokens.Tokens != 42 || r.Tokens.Source != SourceEndpoint || f.got.SystemPrompt != "sys" {
		t.Fatalf("%+v %+v", r, f.got)
	}
	f.ok = false
	r, _ = engine(f).Context(context.Background(), in)
	if r.Tokens.Source != SourceEstimate || r.Tokens.Tokens == 42 {
		t.Fatalf("fallback %+v", r)
	}
}

func TestContextSkipsNativeForUnsupportedContent(t *testing.T) {
	f := &fakeNative{count: Count{Tokens: 42, Source: SourceEndpoint}, ok: true}
	in := Input{Provider: "p", Model: "m", Messages: conv(t, `{"role":"assistant","stopReason":"stop","content":[{"type":"thinking","thinking":"x","thinkingSignature":"sig"}]}`)}
	if _, ok := engine(f).Context(context.Background(), in); !ok || f.got != nil {
		t.Fatalf("native must not see signed thinking: %+v", f.got)
	}
}

func TestContextCancelledReportsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := engine(EstimateCounter{DefaultRatios}).Context(ctx, Input{Provider: "p", Model: "m"}); ok {
		t.Fatal("cancelled context must not report")
	}
}

func TestBreakdownGroupsByRole(t *testing.T) {
	in := Input{
		Provider: "p", Model: "m", LiveSystem: "abcd",
		Messages: conv(t, `{"role":"user","content":"abcd"}`, `{"role":"assistant","content":[{"type":"text","text":"abcd"}]}`, `{"role":"user","content":"abcd"}`, `{"role":"bashExecution","command":"x","excludeFromContext":true}`),
	}
	parts, ok := engine(EstimateCounter{DefaultRatios}).Breakdown(context.Background(), in)
	if !ok || len(parts) != 4 {
		t.Fatalf("%+v", parts)
	}
	if parts[2].Kind != PartUser || parts[2].Items != 2 || parts[2].Count.Tokens != 2 || parts[3].Kind != PartAssistant {
		t.Fatalf("%+v", parts)
	}
}

func TestContextEstimateInheritsNativeDiagnostics(t *testing.T) {
	d := Diagnostic{Provider: "claude", Message: "run /login claude"}
	f := &fakeNative{count: Count{Diagnostics: []Diagnostic{d}}, ok: false}
	r, _ := engine(f).Context(context.Background(), Input{Provider: "p", Model: "m", LiveSystem: "sys", Messages: conv(t, `{"role":"user","content":"hello"}`)})
	if r.Tokens.Source != SourceEstimate || len(r.Tokens.Diagnostics) != 1 || r.Tokens.Diagnostics[0] != d {
		t.Fatalf("%+v", r.Tokens)
	}
}
