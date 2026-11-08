package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

func TestBillingBlockKnownSuffix(t *testing.T) {
	want := "x-anthropic-billing-header: cc_version=2.1.87.6ff; cc_entrypoint=cli;"
	for _, content := range []any{
		"hello world test message",
		[]any{
			map[string]any{"type": "image", "text": "ignore"},
			map[string]any{"type": "text", "text": "hello world test message"},
		},
	} {
		msgs := []any{
			map[string]any{"role": "assistant", "content": "ignore"},
			map[string]any{"role": "user", "content": content},
		}
		if got, ok := billingBlock(msgs, "2.1.87"); !ok || got != want {
			t.Fatalf("billingBlock = %q, %v", got, ok)
		}
	}
	if _, ok := billingBlock([]any{map[string]any{"role": "assistant"}}, "2.1.87"); ok {
		t.Fatal("billing block without a user message")
	}
}

func TestBillingBlockSamplesUTF16(t *testing.T) {
	sum := sha256.Sum256([]byte("59cf53e54c78\ufffdy02.1.87"))
	want := "cc_version=2.1.87." + hex.EncodeToString(sum[:])[:3] + ";"
	got, _ := billingBlock([]any{map[string]any{"role": "user", "content": "abcd😀xy"}}, "2.1.87")
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSanitizeRules(t *testing.T) {
	in := strings.Join([]string{
		"Here is some useful information about the environment you are running in:",
		"Trust if OpenCode honestly reports errors.",
		"drop this paragraph",
		"drop another paragraph",
		"a.b a.b",
	}, "\n\n")
	got := SanitizeSystemText(in, []config.ReplacePattern{
		{Match: "drop"},
		{Match: "the assistant", Replacement: "the helper", HasReplacement: true},
		{Match: "a.b", Replacement: "$&", HasReplacement: true},
	})
	want := "Environment context you are running in:\n\nTrust if the helper honestly reports errors.\n\n$& a.b"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestWithIdentityNormalizes(t *testing.T) {
	identity := map[string]any{"type": "text", "text": Identity}
	for _, sys := range []any{nil, 42.0, false, Identity, []any{map[string]any{"type": "text", "text": Identity}}} {
		got := withIdentity(sys, nil)
		if !reflect.DeepEqual(got, []any{identity}) {
			t.Fatalf("withIdentity(%v) = %v", sys, got)
		}
	}
	cc := map[string]any{"type": "ephemeral"}
	got := withIdentity(map[string]any{"text": "Keep", "cache_control": cc}, nil)
	want := []any{
		map[string]any{"type": "text", "text": Identity, "cache_control": cc},
		map[string]any{"type": "text", "text": "Keep", "cache_control": cc},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestShapeBody(t *testing.T) {
	system := strings.Join([]string{
		"You are an expert coding assistant operating inside a terminal session.",
		"You are operating inside `PiG`, with delegated tools.",
		"Read the harness guide at github.com/earendil-works/pi before starting.",
		"Here is some useful information about the environment you are running in:\n/home/pi/api",
		"Project instructions: preserve widgets and use local tests.",
		"Private workspace note: remove this paragraph.",
	}, "\n\n")
	placeholder := map[string]any{"name": piPlaceholder, "description": "x", "input_schema": map[string]any{}, "cache_control": map[string]any{"type": "ephemeral"}}
	req := map[string]any{
		"system": system,
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "hello world test message"},
				map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{map[string]any{"type": "tool_reference", "tool_name": "web_search"}}},
			}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "bash", "input": map[string]any{"name": "untouched"}},
			}},
			map[string]any{"role": "system", "content": []any{
				map[string]any{"type": "tool_addition", "tool": map[string]any{"type": "tool_definition", "definition": map[string]any{"name": "ask_user"}}},
				map[string]any{"type": "tool_removal", "tool": map[string]any{"type": "tool_reference", "name": "web_search"}},
			}},
		},
		"tools":       []any{map[string]any{"name": "bash"}, map[string]any{"name": "web_search"}, map[string]any{"name": "MyCustomTool"}, map[string]any{"name": "mcp__srv__x"}, placeholder},
		"tool_choice": map[string]any{"type": "tool", "name": "bash"},
		"max_tokens":  json.Number("1000"),
	}
	raw, _ := json.Marshal(req)
	out, restore, err := shapeBody(raw, "2.1.87", []config.ReplacePattern{{Match: "Private workspace note"}, {Match: "widgets", Replacement: "gadgets", HasReplacement: true}}, `{"device_id":"d"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	sys := got["system"].([]any)
	if len(sys) != 3 || !strings.HasPrefix(sys[0].(map[string]any)["text"].(string), "x-anthropic-billing-header: cc_version=2.1.87.6ff") || sys[1].(map[string]any)["text"] != Identity {
		t.Fatalf("system = %v", sys)
	}
	if text := sys[2].(map[string]any)["text"]; text != "Environment context you are running in:\n/home/pi/api\n\nProject instructions: preserve gadgets and use local tests." {
		t.Fatalf("sanitized = %q", text)
	}
	var names []string
	for _, tool := range got["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if !reflect.DeepEqual(names, []string{"Bash", "WebSearch", "MyCustomTool", "mcp__srv__x", "DeferredToolPlaceholder"}) {
		t.Fatalf("tools = %v", names)
	}
	if got["tool_choice"].(map[string]any)["name"] != "Bash" || got["metadata"].(map[string]any)["user_id"] != `{"device_id":"d"}` {
		t.Fatalf("tool_choice/metadata = %v %v", got["tool_choice"], got["metadata"])
	}
	msgs := got["messages"].([]any)
	use := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if use["name"] != "Bash" || use["input"].(map[string]any)["name"] != "untouched" {
		t.Fatalf("tool_use = %v", use)
	}
	if def := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)["tool"].(map[string]any)["definition"].(map[string]any); def["name"] != "AskUser" {
		t.Fatalf("addition = %v", def)
	}
	if !strings.Contains(string(out), `"max_tokens":1000`) {
		t.Fatal("number not preserved")
	}
	for wire, name := range map[string]string{"Bash": "bash", "AskUser": "ask_user", "DeferredToolPlaceholder": piPlaceholder, "Other": "Other"} {
		if r := restore.restore(wire); r != name {
			t.Fatalf("restore(%s) = %s", wire, r)
		}
	}
}

func TestShapeBodyPassThroughAndCollision(t *testing.T) {
	for _, body := range []string{"{", "null", "[]", "42"} {
		out, _, err := shapeBody([]byte(body), "1.2.3", nil, "")
		if err != nil || string(out) != body {
			t.Fatalf("%s -> %s %v", body, out, err)
		}
	}
	_, _, err := shapeBody([]byte(`{"tools":[{"name":"web_search"},{"name":"web-search"}]}`), "1", nil, "")
	if _, ok := err.(*ToolNameCollisionError); !ok {
		t.Fatalf("err = %v", err)
	}
	// A removed tool that once used a now-taken wire name must not block the request.
	if _, _, err := shapeBody([]byte(`{"tools":[{"name":"web_search"}],"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"web-search"}]}]}`), "1", nil, ""); err != nil {
		t.Fatal(err)
	}
}

func TestShapeHeadersAndURL(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-beta", " custom,oauth-2025-04-20,custom, ")
	h.Set("x-api-key", "k")
	h.Set(AccountHeader, "acc")
	h.Set("Content-Length", "9")
	shapeHeaders(h, "tok", "2.1.87", "sess")
	if h.Get("Authorization") != "Bearer tok" || h.Get("x-api-key") != "" || h.Get(AccountHeader) != "" || h.Get("Content-Length") != "" {
		t.Fatalf("headers = %v", h)
	}
	if b := h.Get("anthropic-beta"); !strings.HasPrefix(b, "claude-code-20250219,oauth-2025-04-20,") || !strings.HasSuffix(b, ",custom") || strings.Count(b, "custom") != 1 {
		t.Fatalf("beta = %s", b)
	}
	if h.Get("User-Agent") != "claude-cli/2.1.87 (external, cli)" || h.Get("x-claude-code-session-id") != "sess" {
		t.Fatalf("ua = %v", h)
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	u, _ := url.Parse("https://api.anthropic.com/v1/messages")
	shapeURL(u, customBase(env(map[string]string{"ANTHROPIC_BASE_URL": "http://proxy:8080/ignored"})))
	if u.String() != "http://proxy:8080/v1/messages?beta=true" {
		t.Fatalf("url = %s", u)
	}
	if customBase(env(map[string]string{"ANTHROPIC_BASE_URL": "https://u:p@x"})) != nil || customBase(env(map[string]string{"ANTHROPIC_BASE_URL": "ftp://x"})) != nil {
		t.Fatal("unsafe base accepted")
	}
	if insecureTLS(env(map[string]string{"ANTHROPIC_INSECURE": "1"})) || !insecureTLS(env(map[string]string{"ANTHROPIC_INSECURE": "true", "ANTHROPIC_BASE_URL": "https://x"})) {
		t.Fatal("insecure scoping")
	}
}

func TestRestoreResponseSSE(t *testing.T) {
	r, _ := newRestorer([]string{"bash", "ask_user"}, nil)
	in := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\" : \"AskUser\",\"input\":{}}}\n\n" +
		"data: {\"name\":\"Unknown\",\"x\":\"\\\"name\\\":\\\"Bash\\\"\"}\n"
	resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Length": {"1"}}, Body: io.NopCloser(strings.NewReader(in))}
	out, _ := io.ReadAll(restoreResponse(resp, r).Body)
	if !strings.Contains(string(out), `"name" : "ask_user"`) || !strings.Contains(string(out), `"name":"Unknown"`) || resp.Header.Get("Content-Length") != "" {
		t.Fatalf("out = %s", out)
	}
}

func TestVersionResolution(t *testing.T) {
	cases := []struct {
		env, probe, want string
		warn             bool
	}{
		{"", "", PinnedVersion, false},
		{"", "2.1.99 (Claude Code)", PinnedVersion, false},
		{"", "9.0.0 (Claude Code)", "9.0.0", false},
		{"9.9.9", "", "9.9.9", false},
		{"1.0.0", "", "1.0.0", true},
		{"bad", "", PinnedVersion, true},
	}
	for _, c := range cases {
		v := &version{env: func(string) string { return c.env }, probe: func() string { return c.probe }}
		if got := v.get(); got != c.want || (v.warning != "") != c.warn {
			t.Fatalf("%+v: got %s warn %q", c, got, v.warning)
		}
	}
}

func TestIdentityPersistsDevice(t *testing.T) {
	root := t.TempDir()
	legacy := strings.Repeat("ab", 32)
	_ = os.MkdirAll(filepath.Join(root, "pi-auth-plus"), 0o700)
	_ = os.WriteFile(filepath.Join(root, "pi-auth-plus", "claude-device-id"), []byte(legacy), 0o600)
	dir := func() (string, error) { return filepath.Join(root, "pig-auth-plus"), nil }
	a := newIdentity(dir).deviceID()
	b := newIdentity(dir).deviceID()
	if a != b || a != legacy {
		t.Fatalf("device ids %s %s", a, b)
	}
	id := newIdentity(dir)
	if id.sessionID("not-a-uuid") != id.sessionID("") {
		t.Fatal("fallback session not stable")
	}
	u := "123e4567-e89b-42d3-a456-426614174000"
	if id.sessionID(u) != u {
		t.Fatal("host uuid not kept")
	}
}
