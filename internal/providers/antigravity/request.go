package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// dummySignature is the token Cloud Code accepts in place of a lost thought signature.
const dummySignature = "skip_thought_signature_validator"

// Envelope inputs for one request.
type Envelope struct {
	Model     string // public model id (family)
	Level     Level
	Project   string
	MaxTokens int // model default when the request sets none
	Meta      func(step int) Metadata
}

// Wrap turns a plain Gemini GenerateContent body into the Cloud Code agent envelope.
func (e Envelope) Wrap(in map[string]any) map[string]any {
	contents, _ := in["contents"].([]any)
	if strings.HasPrefix(e.Model, "claude-") {
		contents = stripForeignThinking(contents)
	}
	request := map[string]any{"contents": contents}
	if sys, ok := in["systemInstruction"].(map[string]any); ok {
		request["systemInstruction"] = map[string]any{"parts": sys["parts"]}
	}
	if tools := e.tools(in["tools"]); len(tools) > 0 {
		request["tools"] = tools
		if !strings.HasPrefix(e.Model, "gemini-4-argon") {
			request["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "VALIDATED"}}
		}
	}
	gen, _ := in["generationConfig"].(map[string]any)
	maxOut := any(e.MaxTokens)
	if v, ok := gen["maxOutputTokens"]; ok {
		maxOut = v
	}
	request["generationConfig"] = map[string]any{"maxOutputTokens": maxOut, "thinkingConfig": e.Level.Thinking.Config()}
	meta := e.Meta(len(contents) - 1)
	request["labels"] = meta.Labels
	request["sessionId"] = meta.SessionID
	return map[string]any{
		"project": e.Project, "requestId": meta.RequestID, "request": request,
		"model": e.Level.ID, "userAgent": "antigravity", "requestType": "agent",
	}
}

// tools renames PiG's declarations to Cloud Code's protobuf form.
func (e Envelope) tools(raw any) []any {
	list, _ := raw.([]any)
	moveNumeric := strings.HasPrefix(e.Model, "gpt-oss-")
	var out []any
	for _, t := range list {
		tool, _ := t.(map[string]any)
		decls, _ := tool["functionDeclarations"].([]any)
		if len(decls) == 0 {
			continue
		}
		next := make([]any, 0, len(decls))
		for _, d := range decls {
			decl, ok := d.(map[string]any)
			if !ok {
				continue
			}
			converted := map[string]any{}
			for k, v := range decl {
				switch k {
				case "parametersJsonSchema", "parameters":
					converted["parameters"] = GeminiSchema(v, moveNumeric)
				default:
					converted[k] = v
				}
			}
			next = append(next, converted)
		}
		out = append(out, map[string]any{"functionDeclarations": next})
	}
	return out
}

// stripForeignThinking removes thinking and signatures from Claude history; Anthropic needs
// only the latest signed thinking of an open tool loop.
func stripForeignThinking(contents []any) []any {
	latest := -1
	for i := len(contents) - 1; i >= 0; i-- {
		if c, ok := contents[i].(map[string]any); ok && c["role"] == "model" {
			latest = i
			break
		}
	}
	open := latest >= 0 && hasPart(contents[latest], "functionCall")
	for _, c := range contents[latest+1:] {
		if isUserInput(c) {
			open = false
		}
	}
	out := make([]any, len(contents))
	for i, raw := range contents {
		c, ok := raw.(map[string]any)
		if !ok || c["role"] != "model" {
			out[i] = raw
			continue
		}
		parts, _ := c["parts"].([]any)
		kept := make([]any, 0, len(parts))
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				kept = append(kept, p)
				continue
			}
			thought := part["thought"] == true
			if open && i == latest {
				if thought && part["thoughtSignature"] == nil {
					continue
				}
				kept = append(kept, part)
				continue
			}
			if thought {
				continue
			}
			if _, signed := part["thoughtSignature"]; signed {
				part = cloneWithout(part, "thoughtSignature")
			}
			kept = append(kept, part)
		}
		next := cloneWithout(c)
		next["parts"] = kept
		out[i] = next
	}
	return out
}

// repairSignatures gives the first function call of each model turn after the first user
// input the dummy signature when it has none.
func repairSignatures(contents []any) ([]any, int) {
	user := -1
	for i, c := range contents {
		if isUserInput(c) {
			user = i
			break
		}
	}
	if user < 0 {
		return contents, 0
	}
	repaired := 0
	out := contents
	for i := user + 1; i < len(contents); i++ {
		c, ok := contents[i].(map[string]any)
		if !ok || c["role"] != "model" {
			continue
		}
		parts, _ := c["parts"].([]any)
		for j, p := range parts {
			part, ok := p.(map[string]any)
			if !ok || part["functionCall"] == nil {
				continue
			}
			if s, _ := part["thoughtSignature"].(string); s != "" {
				break
			}
			if repaired == 0 {
				out = append([]any(nil), contents...)
			}
			nextParts := append([]any(nil), parts...)
			signed := cloneWithout(part)
			signed["thoughtSignature"] = dummySignature
			nextParts[j] = signed
			next := cloneWithout(c)
			next["parts"] = nextParts
			out[i] = next
			repaired++
			break
		}
	}
	return out, repaired
}

// isUserInput is a user turn with content other than function responses.
func isUserInput(raw any) bool {
	c, ok := raw.(map[string]any)
	if !ok || c["role"] != "user" {
		return false
	}
	parts, _ := c["parts"].([]any)
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok || part["functionResponse"] != nil || part["functionCall"] != nil {
			continue
		}
		for k := range part {
			if k != "thought" && k != "thoughtSignature" {
				return true
			}
		}
	}
	return false
}

func hasPart(raw any, key string) bool {
	c, _ := raw.(map[string]any)
	parts, _ := c["parts"].([]any)
	for _, p := range parts {
		if part, ok := p.(map[string]any); ok && part[key] != nil {
			return true
		}
	}
	return false
}

func cloneWithout(m map[string]any, drop ...string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
}

// decodeObject keeps numbers exact, so tool arguments survive the rewrite unchanged.
func decodeObject(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	return out, nil
}

// RepairHistory applies the history-wide signature repair to an envelope; false when nothing
// needed repair.
func RepairHistory(envelope []byte) ([]byte, bool) {
	env, err := decodeObject(envelope)
	if err != nil {
		return nil, false
	}
	request, _ := env["request"].(map[string]any)
	contents, _ := request["contents"].([]any)
	repaired, n := repairSignatures(contents)
	if n == 0 {
		return nil, false
	}
	request["contents"] = repaired
	out, err := json.Marshal(env)
	return out, err == nil
}
