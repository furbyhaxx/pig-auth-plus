package antigravity

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

const testToken = "ya29.test-access-token-0123456789"

type fakeCloudCode struct {
	mu     sync.Mutex
	bodies []map[string]any
	reply  func(n int) (int, string)
}

func (f *fakeCloudCode) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if strings.HasSuffix(r.URL.Path, ":loadCodeAssist") {
			_, _ = io.WriteString(w, `{"cloudaicompanionProject":"proj-secret-42"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		n := len(f.bodies)
		f.mu.Unlock()
		status, payload := f.reply(n)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	})
}

func sse(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: {\"response\":" + c + "}\n\n")
	}
	return b.String()
}

func testProvider(t *testing.T, f *fakeCloudCode) *Provider {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	cat, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return &Provider{
		Catalog: cat, Projects: &Projects{BaseURL: srv.URL}, Sessions: &Sessions{},
		Runtime: wire.NewRuntime(func() (string, error) { return t.TempDir(), nil }), BaseURL: srv.URL, Client: srv.Client(),
	}
}

var testModel = map[string]any{
	"id": "gemini-3.8-flash", "provider": "antigravity", "api": "google-generative-ai", "maxTokens": float64(65536),
	"input": []any{"text", "image"}, "cost": map[string]any{"input": 0.0, "output": 0.0},
	"thinkingLevelMap": map[string]any{"low": "gemini-3.8-flash-low|M320|level:LOW", "high": "gemini-3.8-flash-high|M318|level:HIGH"},
}

func testRequest() map[string]any {
	return map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "", "sections": map[string]any{"preamble": "You help."},
			"toolsAdded": []any{map[string]any{"name": "read", "description": "Read", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "read go.mod"}}, "timestamp": float64(1)},
	}}
}

func collect(t *testing.T, s *sdk.ModelEventStream) ([]string, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var types []string
	for e := range s.Events(ctx) {
		types = append(types, e["type"].(string))
	}
	return types, s.Result()
}

func TestStreamMapsThinkingTextAndToolCalls(t *testing.T) {
	f := &fakeCloudCode{reply: func(int) (int, string) {
		return 200, sse(
			`{"responseId":"r1","candidates":[{"content":{"parts":[{"text":"plan","thought":true}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"","thought":true,"thoughtSignature":"c2lnMQ=="}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call-1","name":"read","args":{"path":"go.mod"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":7,"thoughtsTokenCount":3,"cachedContentTokenCount":40,"totalTokenCount":110}}`,
		)
	}}
	p := testProvider(t, f)
	stream, err := p.stream(true, testModel, testRequest(), sdk.ProviderStreamOptions{Values: map[string]any{"apiKey": testToken, "reasoning": "medium", "sessionId": "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	types, msg := collect(t, stream)
	want := "start thinking_start thinking_delta thinking_end toolcall_start toolcall_delta toolcall_end done"
	if strings.Join(types, " ") != want {
		t.Fatalf("events = %v", types)
	}
	if msg["stopReason"] != "toolUse" || msg["provider"] != "antigravity" || msg["responseId"] != "r1" {
		t.Fatalf("message = %v", msg)
	}
	content := msg["content"].([]any)
	if content[0].(map[string]any)["thinkingSignature"] != "c2lnMQ==" || content[1].(map[string]any)["id"] != "call-1" {
		t.Fatalf("content = %v", content)
	}
	u := msg["usage"].(map[string]any)
	if u["input"] != 60 || u["cacheRead"] != 40 || u["output"] != 10 {
		t.Fatalf("usage = %v", u)
	}
	env := f.bodies[0]
	if env["model"] != "gemini-3.8-flash-high" || env["project"] != "proj-secret-42" {
		t.Fatalf("envelope model/project = %v %v", env["model"], env["project"])
	}
	req := env["request"].(map[string]any)
	if req["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "You help." || req["tools"] == nil {
		t.Fatalf("request = %v", req)
	}
}

func TestStreamRecoversAMissingThoughtSignatureOnce(t *testing.T) {
	f := &fakeCloudCode{reply: func(n int) (int, string) {
		if n == 1 {
			return 400, `{"error":{"code":400,"message":"Function call is missing a thought_signature in functionCall parts."}}`
		}
		return 200, sse(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}}
	p := testProvider(t, f)
	req := testRequest()
	req["messages"] = append(req["messages"].([]any),
		map[string]any{"role": "assistant", "provider": "other", "api": "x", "model": "y", "stopReason": "toolUse",
			"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "a"}}}},
		map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": "x"}}},
	)
	stream, _ := p.stream(true, testModel, req, sdk.ProviderStreamOptions{Values: map[string]any{"apiKey": testToken}})
	_, msg := collect(t, stream)
	if msg["stopReason"] != "stop" || len(f.bodies) != 2 {
		t.Fatalf("stop = %v after %d requests: %v", msg["stopReason"], len(f.bodies), msg["errorMessage"])
	}
	b, _ := json.Marshal(f.bodies[1])
	if !strings.Contains(string(b), dummySignature) {
		t.Fatalf("retry was not repaired: %s", b)
	}
}

func TestStreamErrorsAreRedacted(t *testing.T) {
	f := &fakeCloudCode{reply: func(int) (int, string) {
		return 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota for proj-secret-42","details":[{"reason":"RATE_LIMIT_EXCEEDED","retryDelay":"3s"}]}}`
	}}
	p := testProvider(t, f)
	stream, _ := p.stream(true, testModel, testRequest(), sdk.ProviderStreamOptions{Values: map[string]any{"apiKey": testToken}})
	types, msg := collect(t, stream)
	text, _ := msg["errorMessage"].(string)
	if types[len(types)-1] != "error" || !strings.Contains(text, "RATE_LIMIT_EXCEEDED") || strings.Contains(text, "proj-secret-42") {
		t.Fatalf("events %v, error %q", types, text)
	}
}
