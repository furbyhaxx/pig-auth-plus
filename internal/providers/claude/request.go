package claude

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

var requiredBetas = []string{
	"claude-code-20250219",
	"oauth-2025-04-20",
	"interleaved-thinking-2025-05-14",
	"context-management-2025-06-27",
	"per-turn-control-2026-07-01",
	"mid-conversation-tool-changes-2026-07-01",
	"inline-tools-2026-09-15",
	"advisor-tool-2026-03-01",
	"thinking-token-count-2026-05-13",
	"advanced-tool-use-2025-11-20",
	"effort-2025-11-24",
	"extended-cache-ttl-2025-04-11",
}

// shapeBody adds the billing and identity blocks, metadata.user_id and Claude Code tool names,
// and returns the restorer for the response. A body that is not a JSON object passes through.
func shapeBody(body []byte, version string, rules []config.ReplacePattern, userID string) ([]byte, restorer, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var req map[string]any
	if err := dec.Decode(&req); err != nil || req == nil {
		return body, restorer{}, nil
	}
	current, earlier := toolNames(req)
	restore, err := newRestorer(current, earlier)
	if err != nil {
		return nil, nil, err
	}
	if userID != "" {
		req["metadata"] = map[string]any{"user_id": userID}
	}
	system := withIdentity(req["system"], rules)
	if billing, ok := billingBlock(req["messages"], version); ok {
		system = append([]any{map[string]any{"type": "text", "text": billing}}, system...)
	}
	req["system"] = system
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			if m, ok := t.(map[string]any); ok {
				shapeToolDefinition(m)
			}
		}
	}
	if choice, ok := req["tool_choice"].(map[string]any); ok && choice["type"] == "tool" {
		if name, ok := choice["name"].(string); ok {
			choice["name"] = wireToolName(name)
		}
	}
	renameContent(req["messages"])
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return nil, nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), restore, nil
}

// toolNames returns the request's current tool names (declared or added mid-conversation)
// and the names earlier transcript entries used.
func toolNames(req map[string]any) (current, earlier []string) {
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			if m, ok := t.(map[string]any); ok {
				if name, ok := m["name"].(string); ok && name != "" {
					current = append(current, name)
				}
			}
		}
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			switch x["type"] {
			case "tool_use":
				if name, ok := x["name"].(string); ok && name != "" {
					earlier = append(earlier, name)
				}
			case "tool_addition":
				if tool, ok := x["tool"].(map[string]any); ok && tool["type"] == "tool_definition" {
					if def, ok := tool["definition"].(map[string]any); ok {
						if name, ok := def["name"].(string); ok && name != "" && !slices.Contains(current, name) {
							current = append(current, name)
						}
					}
				}
			}
			if c, ok := x["content"]; ok {
				walk(c)
			}
		}
	}
	walk(req["messages"])
	return current, earlier
}

func shapeToolDefinition(tool map[string]any) {
	name, _ := tool["name"].(string)
	if name == "" {
		return
	}
	if name == piPlaceholder {
		cc, hasCC := tool["cache_control"]
		clear(tool)
		tool["name"] = "DeferredToolPlaceholder"
		tool["description"] = "Reserved placeholder that keeps deferred tool loading active; never call this tool."
		tool["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
		tool["defer_loading"] = true
		if hasCC {
			tool["cache_control"] = cc
		}
		return
	}
	tool["name"] = wireToolName(name)
}

func renameContent(v any) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			renameContent(item)
		}
	case map[string]any:
		switch x["type"] {
		case "tool_reference":
			if n, ok := x["tool_name"].(string); ok {
				x["tool_name"] = wireToolName(n)
			}
		case "tool_use":
			if n, ok := x["name"].(string); ok && n != "" {
				x["name"] = wireToolName(n)
			}
		case "tool_addition":
			if tool, ok := x["tool"].(map[string]any); ok && tool["type"] == "tool_definition" {
				if def, ok := tool["definition"].(map[string]any); ok {
					shapeToolDefinition(def)
				}
			}
		case "tool_removal":
			if tool, ok := x["tool"].(map[string]any); ok && tool["type"] == "tool_reference" {
				if n, ok := tool["name"].(string); ok {
					tool["name"] = wireToolName(n)
				}
			}
		}
		if c, ok := x["content"]; ok {
			renameContent(c)
		}
	}
}

// shapeHeaders sets OAuth, beta, user-agent and session headers; version must match shapeBody's.
func shapeHeaders(h http.Header, token, version, sessionID string) {
	var incoming []string
	for _, v := range h.Values("anthropic-beta") {
		for b := range strings.SplitSeq(v, ",") {
			if b = strings.TrimSpace(b); b != "" {
				incoming = append(incoming, b)
			}
		}
	}
	betas := slices.Clone(requiredBetas)
	for _, b := range incoming {
		if !slices.Contains(betas, b) {
			betas = append(betas, b)
		}
	}
	h.Set("Authorization", "Bearer "+token)
	h.Set("anthropic-beta", strings.Join(betas, ","))
	h.Set("User-Agent", UserAgent(version))
	h.Set("x-app", "cli")
	h.Set("x-claude-code-session-id", sessionID)
	h.Del("x-api-key")
	h.Del(AccountHeader)
	for _, k := range []string{"Content-Length", "Content-Md5", "Content-Digest", "Digest"} {
		h.Del(k)
	}
}

// customBase is ANTHROPIC_BASE_URL when it is HTTP(S) without embedded credentials.
func customBase(env func(string) string) *url.URL {
	raw := strings.TrimSpace(env("ANTHROPIC_BASE_URL"))
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Host == "" {
		return nil
	}
	return u
}

// shapeURL replaces only the origin and opts messages requests into beta.
func shapeURL(u *url.URL, base *url.URL) {
	if base != nil {
		u.Scheme, u.Host = base.Scheme, base.Host
	}
	if u.Path == "/v1/messages" {
		q := u.Query()
		if !q.Has("beta") {
			q.Set("beta", "true")
			u.RawQuery = q.Encode()
		}
	}
}

// insecureTLS is scoped to an explicitly configured custom endpoint.
func insecureTLS(env func(string) string) bool {
	if customBase(env) == nil {
		return false
	}
	v := strings.TrimSpace(env("ANTHROPIC_INSECURE"))
	return v == "1" || v == "true"
}
