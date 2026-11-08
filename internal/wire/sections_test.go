package wire

import (
	"encoding/json"
	"testing"
)

func TestOrderSectionsFollowsTheComposedPrompt(t *testing.T) {
	request := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "", "sections": map[string]any{
			"addendum": "ADD", "preamble": "PRE", "tools": "TOOLS", "extra": "X-unknown", "gone": nil,
		}},
		map[string]any{"role": "user", "content": "hi"},
	}}
	out := OrderSections(request, "PRE\n\nTOOLS\n\nADD")
	b, err := json.Marshal(out["messages"].([]any)[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":"","role":"system","sections":{"preamble":"PRE","tools":"TOOLS","addendum":"ADD","extra":"X-unknown","gone":null}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if _, ok := request["messages"].([]any)[0].(map[string]any)["sections"].(map[string]any); !ok {
		t.Fatal("caller's request was mutated")
	}
	if got := SystemSections(out["messages"].([]any)[0].(map[string]any)).Text(); got != "PRE\n\nTOOLS\n\nADD\n\nX-unknown" {
		t.Fatalf("text = %q", got)
	}
}
