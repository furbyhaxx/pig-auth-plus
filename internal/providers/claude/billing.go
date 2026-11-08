package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode/utf16"
)

const billingSalt = "59cf53e54c78"

var billingSamples = []int{4, 7, 20}

// billingBlock derives Claude Code's billing system text from the first user message. It
// returns false without a user message; a user message without text hashes the empty string.
func billingBlock(messages any, version string) (string, bool) {
	list, _ := messages.([]any)
	for _, m := range list {
		msg, ok := m.(map[string]any)
		if !ok || msg["role"] != "user" {
			continue
		}
		// The CLI samples UTF-16 code units; a lone surrogate hashes as U+FFFD like Node's UTF-8.
		units := utf16.Encode([]rune(firstText(msg["content"])))
		sample := make([]rune, 0, len(billingSamples))
		for _, p := range billingSamples {
			if p < len(units) {
				sample = append(sample, rune(units[p]))
			} else {
				sample = append(sample, '0')
			}
		}
		sum := sha256.Sum256([]byte(billingSalt + string(sample) + version))
		return fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli;", version, hex.EncodeToString(sum[:])[:3]), true
	}
	return "", false
}

func firstText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		for _, b := range c {
			if block, ok := b.(map[string]any); ok && block["type"] == "text" {
				s, _ := block["text"].(string)
				return s
			}
		}
	}
	return ""
}
