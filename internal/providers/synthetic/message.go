package synthetic

import (
	"encoding/json"
	"maps"
	"math"
	"strings"
)

// PromptCounter returns the real token count of a request whose prompt Synthetic reported as
// reported units, or false when it cannot tell.
type PromptCounter func(model string, reported int, payload []byte) (int, bool)

// finalize folds interleaved blocks and corrects usage on the final message of one request.
func finalize(message map[string]any, model string, rates map[string]any, payload []byte, count PromptCounter) map[string]any {
	out := maps.Clone(message)
	if content, ok := foldInterleaved(message["content"]); ok {
		out["content"] = content
	}
	if usage, ok := message["usage"].(map[string]any); ok {
		out["usage"] = correctUsage(usage, model, rates, payload, count)
	}
	return out
}

// foldInterleaved merges Synthetic's alternating reasoning and answer fragments (DeepSeek V4)
// into one thinking and one text block, each anchored where its kind first appears. Tool calls
// keep their order. Signed or redacted reasoning is opaque replay data and is never merged.
func foldInterleaved(raw any) ([]any, bool) {
	content, ok := raw.([]any)
	if !ok || len(content) < 2 {
		return nil, false
	}
	var thinking, text strings.Builder
	nThinking, nText := 0, 0
	for _, entry := range content {
		block, _ := entry.(map[string]any)
		switch block["type"] {
		case "thinking":
			if sig, _ := block["thinkingSignature"].(string); sig != "" || block["redacted"] == true {
				return nil, false
			}
			s, _ := block["thinking"].(string)
			thinking.WriteString(s)
			nThinking++
		case "text":
			s, _ := block["text"].(string)
			text.WriteString(s)
			nText++
		}
	}
	if nThinking < 2 && nText < 2 {
		return nil, false
	}
	folded := make([]any, 0, len(content))
	seenThinking, seenText := false, false
	for _, entry := range content {
		block, _ := entry.(map[string]any)
		switch block["type"] {
		case "thinking":
			if !seenThinking {
				seenThinking = true
				folded = append(folded, map[string]any{"type": "thinking", "thinking": thinking.String()})
			}
		case "text":
			if !seenText {
				seenText = true
				merged := maps.Clone(block)
				merged["text"] = text.String()
				folded = append(folded, merged)
			}
		default:
			folded = append(folded, entry)
		}
	}
	return folded, true
}

// correctUsage drops the cache write Synthetic duplicates from the uncached input, then
// rescales the prompt to real tokens. Output tokens are exact. Cost is priced from rates (dollars
// per million) on the reported units, which Synthetic's prices refer to; nil rates keep the
// stream's cost.
func correctUsage(raw map[string]any, model string, rates map[string]any, payload []byte, count PromptCounter) map[string]any {
	usage := maps.Clone(raw)
	input, cacheRead, cacheWrite, output := num(usage["input"]), num(usage["cacheRead"]), num(usage["cacheWrite"]), num(usage["output"])
	if cacheWrite > 0 && cacheWrite == input {
		cacheWrite = 0
		usage["cacheWrite"] = 0
		if _, ok := usage["cacheWrite1h"]; ok {
			usage["cacheWrite1h"] = 0
		}
	}
	if rates != nil {
		cost, _ := usage["cost"].(map[string]any)
		cost = maps.Clone(cost)
		if cost == nil {
			cost = map[string]any{}
		}
		parts := [4]float64{
			float64(input) * fnum(rates["input"]) / 1e6,
			float64(output) * fnum(rates["output"]) / 1e6,
			float64(cacheRead) * fnum(rates["cacheRead"]) / 1e6,
			float64(cacheWrite) * fnum(rates["cacheWrite"]) / 1e6,
		}
		cost["input"], cost["output"], cost["cacheRead"], cost["cacheWrite"] = parts[0], parts[1], parts[2], parts[3]
		cost["total"] = parts[0] + parts[1] + parts[2] + parts[3]
		usage["cost"] = cost
	}
	prompt := input + cacheRead + cacheWrite
	if prompt > 0 && count != nil {
		if real, ok := count(model, prompt, payload); ok && real > 0 {
			scaledInput := int(math.Round(float64(input) * float64(real) / float64(prompt)))
			scaledWrite := int(math.Round(float64(cacheWrite) * float64(real) / float64(prompt)))
			input, cacheWrite, cacheRead = scaledInput, scaledWrite, real-scaledInput-scaledWrite
			usage["input"], usage["cacheWrite"], usage["cacheRead"] = input, cacheWrite, cacheRead
		}
	}
	usage["totalTokens"] = input + cacheRead + cacheWrite + output
	return usage
}

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func fnum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
