package claude

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdaptCountDropsGenerationFieldsAndBillingBlock(t *testing.T) {
	body := `{"model":"m","max_tokens":10,"stream":true,"temperature":1,"metadata":{"user_id":"u"},` +
		`"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=1.2.3; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"a<b"}]}]}`
	h := http.Header{"Content-Length": {"9"}, "Anthropic-Beta": {"x"}, "Authorization": {"Bearer t"}}
	got, ok := adaptCount(countRequest{url: "https://api.anthropic.com/v1/messages?beta=true", header: h, body: []byte(body)}, "t", true)
	if !ok || got.url != "https://api.anthropic.com/v1/messages/count_tokens?beta=true" {
		t.Fatalf("%+v %v", got, ok)
	}
	var out map[string]any
	if err := json.Unmarshal(got.body, &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"max_tokens", "stream", "temperature", "metadata"} {
		if _, has := out[k]; has {
			t.Fatalf("%s must be dropped", k)
		}
	}
	system := out["system"].([]any)
	if len(system) != 1 || system[0].(map[string]any)["text"] != "You are Claude Code" {
		t.Fatalf("billing block must be dropped: %v", system)
	}
	if string(got.body) == "" || !json.Valid(got.body) || got.header.Get("Content-Length") != "" || got.header.Get("Anthropic-Beta") != "x" {
		t.Fatalf("%s %v", got.body, got.header)
	}
	if !strings.Contains(string(got.body), `"a<b"`) {
		t.Fatalf("HTML must not be escaped: %s", got.body)
	}
}

func TestAdaptCountKeepsFirstSystemBlockForAPIKeys(t *testing.T) {
	body := `{"model":"m","system":[{"type":"text","text":"x-anthropic-billing-header: user text"}],"messages":[]}`
	got, ok := adaptCount(countRequest{url: "https://x/v1/messages", header: http.Header{}, body: []byte(body)}, "k", false)
	if !ok || got.header.Get("X-Api-Key") != "k" || !strings.Contains(string(got.body), "user text") {
		t.Fatalf("%+v", got)
	}
}

func TestAdaptCountRefusesUnexpectedEndpoint(t *testing.T) {
	if _, ok := adaptCount(countRequest{url: "https://x/other", header: http.Header{}, body: []byte(`{}`)}, "k", false); ok {
		t.Fatal("only /v1/messages requests can become count requests")
	}
}
