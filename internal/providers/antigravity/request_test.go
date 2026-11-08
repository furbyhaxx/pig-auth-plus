package antigravity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapBuildsTheCloudCodeEnvelope(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"systemInstruction":{"role":"user","parts":[{"text":"sys"}]},
		"tools":[{"functionDeclarations":[{"name":"read","description":"Read","parametersJsonSchema":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"}},"required":["path"]}}]}],
		"generationConfig":{"maxOutputTokens":1234,"thinkingConfig":{"thinkingBudget":99}}}`
	env := Envelope{
		Model: "gemini-4-pro", Project: "proj-1", MaxTokens: 9,
		Level: Level{ID: "gemini-4-pro-high", Enum: "MODEL_X", Thinking: Thinking{Kind: "level", Effort: "HIGH"}},
		Meta: func(step int) Metadata {
			if step != 0 {
				t.Fatalf("step = %d", step)
			}
			return Metadata{RequestID: "rid", SessionID: "sid", Labels: map[string]any{"model_enum": "MODEL_X"}}
		},
	}
	in, err := decodeObject([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := roundTrip(t, env.Wrap(in))
	if got["model"] != "gemini-4-pro-high" || got["project"] != "proj-1" || got["requestId"] != "rid" || got["requestType"] != "agent" {
		t.Fatalf("envelope = %v", got)
	}
	req := got["request"].(map[string]any)
	gen := req["generationConfig"].(map[string]any)
	if gen["maxOutputTokens"] != float64(1234) || gen["thinkingConfig"].(map[string]any)["thinkingLevel"] != "HIGH" {
		t.Fatalf("generationConfig = %v", gen)
	}
	decl := req["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	params := decl["parameters"].(map[string]any)
	if params["type"] != "OBJECT" || params["additionalProperties"] != nil || decl["parametersJsonSchema"] != nil {
		t.Fatalf("declaration = %v", decl)
	}
	if req["toolConfig"] == nil || req["sessionId"] != "sid" {
		t.Fatalf("request = %v", req)
	}
	if _, has := req["systemInstruction"].(map[string]any)["role"]; has {
		t.Fatal("systemInstruction kept its role")
	}
}

func TestWrapStripsClaudeHistoryThinkingButKeepsTheOpenToolLoop(t *testing.T) {
	body := `{"contents":[
		{"role":"user","parts":[{"text":"q1"}]},
		{"role":"model","parts":[{"text":"old thought","thought":true,"thoughtSignature":"s1"},{"text":"a1","thoughtSignature":"s2"}]},
		{"role":"user","parts":[{"text":"q2"}]},
		{"role":"model","parts":[{"text":"t","thought":true,"thoughtSignature":"s3"},{"text":"unsigned","thought":true},{"functionCall":{"name":"read","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"read","response":{"output":"x"}}}]}]}`
	env := Envelope{Model: "claude-sonnet-5", Level: Level{ID: "claude", Enum: "E", Thinking: Thinking{Kind: "adaptive", Effort: "LOW"}}, Meta: func(int) Metadata { return Metadata{} }}
	in, err := decodeObject([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(env.Wrap(in))
	s := string(out)
	if strings.Contains(s, "old thought") || strings.Contains(s, `"s2"`) || strings.Contains(s, "unsigned") {
		t.Fatalf("history kept stale thinking: %s", s)
	}
	if !strings.Contains(s, `"s3"`) {
		t.Fatalf("open tool loop lost its signed thinking: %s", s)
	}
}

func TestRepairHistorySignsUnsignedCallsOnce(t *testing.T) {
	env := `{"request":{"contents":[
		{"role":"user","parts":[{"text":"q"}]},
		{"role":"model","parts":[{"functionCall":{"name":"a","args":{"n":1}}},{"functionCall":{"name":"b","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"a","response":{}}}]},
		{"role":"model","parts":[{"functionCall":{"name":"c","args":{}},"thoughtSignature":"real"}]}]}}`
	out, ok := RepairHistory([]byte(env))
	if !ok {
		t.Fatal("nothing repaired")
	}
	if n := strings.Count(string(out), dummySignature); n != 1 {
		t.Fatalf("dummy signatures = %d in %s", n, out)
	}
	if _, again := RepairHistory(out); again {
		t.Fatal("repair is not idempotent")
	}
}

func TestMissingSignatureDetection(t *testing.T) {
	body := []byte(`{"error":{"code":400,"message":"Function call is missing a thought_signature in functionCall parts."}}`)
	if !isMissingSignature(400, body) || isMissingSignature(429, body) || isMissingSignature(400, []byte(`{"error":{"message":"bad"}}`)) {
		t.Fatal("detection mismatch")
	}
}

func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
