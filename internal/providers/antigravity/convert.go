package antigravity

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// Behavior ported from pi-ai 1.4.0 api/transform-messages.js and api/google-shared.js (MIT),
// through pi-auth-plus src/families/google/{transform,convert}.ts.

const (
	userImagePlaceholder = "(image omitted: model does not support images)"
	toolImagePlaceholder = "(tool image omitted: model does not support images)"
)

// target is the model a request is built for.
type target struct {
	Provider, API, ID string
	Images            bool
}

func targetOf(model map[string]any) target {
	t := target{}
	t.Provider, _ = model["provider"].(string)
	t.API, _ = model["api"].(string)
	t.ID, _ = model["id"].(string)
	for _, in := range anyList(model["input"]) {
		if in == "image" {
			t.Images = true
		}
	}
	return t
}

// GeminiBody builds the plain Gemini request (contents, systemInstruction, tools) for a PiG
// stream request. prompt orders system sections (see wire.OrderSections).
func GeminiBody(model map[string]any, request map[string]any, prompt string, maxTokens int) map[string]any {
	t := targetOf(model)
	messages := anyList(request["messages"])
	system := wire.CurrentSystem(messages, prompt)
	body := map[string]any{"contents": convertMessages(t, transform(t, messages))}
	if system.Prompt != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": sanitize(system.Prompt)}}}
	}
	if len(system.Tools) > 0 {
		decls := make([]any, 0, len(system.Tools))
		for _, tool := range system.Tools {
			decls = append(decls, map[string]any{"name": tool["name"], "description": tool["description"], "parametersJsonSchema": tool["parameters"]})
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	if maxTokens > 0 {
		body["generationConfig"] = map[string]any{"maxOutputTokens": maxTokens}
	}
	return body
}

// transform normalizes content for t: other models' thinking and signatures are dropped,
// tool call ids normalized, failed turns skipped and orphaned tool calls closed.
func transform(t target, messages []any) []map[string]any {
	idMap := map[string]string{}
	var result []map[string]any
	var pending []map[string]any
	answered := map[string]bool{}
	closePending := func() {
		for _, call := range pending {
			id, _ := call["id"].(string)
			if !answered[id] {
				result = append(result, map[string]any{
					"role": "toolResult", "toolCallId": id, "toolName": call["name"],
					"content": []any{map[string]any{"type": "text", "text": "No result provided"}}, "isError": true,
				})
			}
		}
		pending, answered = nil, map[string]bool{}
	}
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch msg["role"] {
		case "user":
			closePending()
			if !t.Images {
				msg = withContent(msg, downgradeImages(msg["content"], userImagePlaceholder))
			}
			result = append(result, msg)
		case "toolResult":
			if id, _ := msg["toolCallId"].(string); idMap[id] != "" {
				msg = withField(msg, "toolCallId", idMap[id])
			}
			if !t.Images {
				msg = withContent(msg, downgradeImages(msg["content"], toolImagePlaceholder))
			}
			id, _ := msg["toolCallId"].(string)
			answered[id] = true
			result = append(result, msg)
		case "assistant":
			closePending()
			if s := msg["stopReason"]; s == "error" || s == "aborted" {
				continue
			}
			same := msg["provider"] == t.Provider && msg["api"] == t.API && msg["model"] == t.ID
			var blocks []any
			for _, b := range anyList(msg["content"]) {
				block, ok := b.(map[string]any)
				if !ok {
					continue
				}
				switch block["type"] {
				case "thinking":
					if !same {
						continue
					}
					text, _ := block["thinking"].(string)
					if block["redacted"] != true && str(block["thinkingSignature"]) == "" && strings.TrimSpace(text) == "" {
						continue
					}
					blocks = append(blocks, block)
				case "text":
					if !same {
						block = map[string]any{"type": "text", "text": block["text"]}
					}
					blocks = append(blocks, block)
				case "toolCall":
					if !same {
						block = cloneWithout(block, "thoughtSignature")
						if id, _ := block["id"].(string); requiresToolCallID(t.ID) {
							if n := normalizeToolCallID(id); n != id {
								idMap[id] = n
								block["id"] = n
							}
						}
					}
					blocks = append(blocks, block)
					pending = append(pending, block)
				}
			}
			result = append(result, withContent(msg, blocks))
		}
	}
	closePending()
	return result
}

// convertMessages turns transformed messages into Gemini contents.
func convertMessages(t target, messages []map[string]any) []any {
	var contents []any
	ids := requiresToolCallID(t.ID)
	for _, msg := range messages {
		switch msg["role"] {
		case "user":
			var parts []any
			if s, ok := msg["content"].(string); ok {
				parts = []any{map[string]any{"text": sanitize(s)}}
			} else {
				for _, b := range anyList(msg["content"]) {
					block, _ := b.(map[string]any)
					switch block["type"] {
					case "text":
						parts = append(parts, map[string]any{"text": sanitize(str(block["text"]))})
					case "image":
						parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": block["mimeType"], "data": block["data"]}})
					}
				}
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "user", "parts": parts})
			}
		case "assistant":
			same := msg["provider"] == t.Provider && msg["model"] == t.ID
			var parts []any
			for _, b := range anyList(msg["content"]) {
				block, _ := b.(map[string]any)
				switch block["type"] {
				case "text":
					text := str(block["text"])
					sig := signature(same, block["textSignature"])
					if strings.TrimSpace(text) == "" && sig == "" {
						continue
					}
					parts = append(parts, withSignature(map[string]any{"text": sanitize(text)}, sig))
				case "thinking":
					text := str(block["thinking"])
					if same {
						sig := signature(true, block["thinkingSignature"])
						if strings.TrimSpace(text) == "" && sig == "" {
							continue
						}
						parts = append(parts, withSignature(map[string]any{"thought": true, "text": sanitize(text)}, sig))
					} else if strings.TrimSpace(text) != "" {
						parts = append(parts, map[string]any{"text": sanitize(text)})
					}
				case "toolCall":
					args, _ := block["arguments"].(map[string]any)
					if args == nil {
						args = map[string]any{}
					}
					call := map[string]any{"name": block["name"], "args": args}
					if ids {
						call["id"] = block["id"]
					}
					parts = append(parts, withSignature(map[string]any{"functionCall": call}, signature(same, block["thoughtSignature"])))
				}
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case "toolResult":
			var texts []string
			var images []any
			for _, b := range anyList(msg["content"]) {
				block, _ := b.(map[string]any)
				switch block["type"] {
				case "text":
					texts = append(texts, str(block["text"]))
				case "image":
					if t.Images {
						images = append(images, map[string]any{"inlineData": map[string]any{"mimeType": block["mimeType"], "data": block["data"]}})
					}
				}
			}
			text := strings.Join(texts, "\n")
			value := sanitize(text)
			if strings.TrimSpace(text) == "" {
				value = ""
				if len(images) > 0 {
					value = "(see attached image)"
				}
			}
			response := map[string]any{"output": value}
			if msg["isError"] == true {
				response = map[string]any{"error": value}
			}
			fr := map[string]any{"name": msg["toolName"], "response": response}
			multimodal := supportsMultimodalFunctionResponse(t.ID)
			if len(images) > 0 && multimodal {
				fr["parts"] = images
			}
			if ids {
				fr["id"] = msg["toolCallId"]
			}
			part := map[string]any{"functionResponse": fr}
			if n := len(contents); n > 0 && isFunctionResponseTurn(contents[n-1]) {
				last := contents[n-1].(map[string]any)
				last["parts"] = append(last["parts"].([]any), part)
			} else {
				contents = append(contents, map[string]any{"role": "user", "parts": []any{part}})
			}
			if len(images) > 0 && !multimodal {
				contents = append(contents, map[string]any{"role": "user", "parts": append([]any{map[string]any{"text": "Tool result image:"}}, images...)})
			}
		}
	}
	return contents
}

func isFunctionResponseTurn(raw any) bool {
	c, _ := raw.(map[string]any)
	if c["role"] != "user" {
		return false
	}
	for _, p := range anyList(c["parts"]) {
		if part, ok := p.(map[string]any); ok && part["functionResponse"] != nil {
			return true
		}
	}
	return false
}

func downgradeImages(content any, placeholder string) any {
	blocks, ok := content.([]any)
	if !ok {
		return content
	}
	var out []any
	previous := false
	for _, b := range blocks {
		block, _ := b.(map[string]any)
		if block["type"] == "image" {
			if !previous {
				out = append(out, map[string]any{"type": "text", "text": placeholder})
			}
			previous = true
			continue
		}
		out = append(out, b)
		previous = block["text"] == placeholder
	}
	return out
}

var base64Signature = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

func signature(same bool, v any) string {
	s, _ := v.(string)
	if !same || s == "" || len(s)%4 != 0 || !base64Signature.MatchString(s) {
		return ""
	}
	return s
}

func withSignature(part map[string]any, sig string) map[string]any {
	if sig != "" {
		part["thoughtSignature"] = sig
	}
	return part
}

var geminiVersion = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

func geminiMajor(id string) (int, bool) {
	m := geminiVersion.FindStringSubmatch(strings.ToLower(id))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// requiresToolCallID reports models whose calls and responses need explicit ids.
func requiresToolCallID(id string) bool {
	v, ok := geminiMajor(id)
	return strings.HasPrefix(id, "claude-") || strings.HasPrefix(id, "gpt-oss-") || ok && v >= 3
}

func supportsMultimodalFunctionResponse(id string) bool {
	v, ok := geminiMajor(id)
	return !ok || v >= 3
}

var toolIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func normalizeToolCallID(id string) string {
	id = toolIDUnsafe.ReplaceAllString(id, "_")
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// sanitize drops lone surrogates; in Go they arrive as U+FFFD from JSON decoding, so this
// only has to keep invalid UTF-8 out.
func sanitize(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

func withContent(msg map[string]any, content any) map[string]any {
	return withField(msg, "content", content)
}

func withField(msg map[string]any, key string, v any) map[string]any {
	out := cloneWithout(msg)
	out[key] = v
	return out
}

func anyList(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
