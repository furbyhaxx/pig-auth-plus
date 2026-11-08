package claude

import (
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

// Identity is the interactive Claude Code CLI's identity line.
const Identity = "You are Claude Code, Anthropic's official CLI for Claude."

var (
	identityPrefixes = []string{
		"You are OpenCode",
		"You are an expert coding assistant operating inside",
		"You are operating inside `Pi`",
		"You are operating inside `PiG`",
	}
	removalAnchors = []string{
		"github.com/anomalyco/opencode",
		"opencode.ai/docs",
		"github.com/opensec-git/Pi-Bolt",
		"github.com/MichaelKinsy/PiG",
		"earendil-works/pi",
		"pi-coding-agent",
	}
	builtinReplacements = []config.ReplacePattern{
		{Match: "if OpenCode honestly", Replacement: "if the assistant honestly", HasReplacement: true},
		{Match: "Here is some useful information about the environment you are running in:", Replacement: "Environment context you are running in:", HasReplacement: true},
	}
)

// SanitizeSystemText drops branded paragraphs, then applies the built-in and user literal
// rewrites in order; each rewrite replaces the first occurrence, as the CLI's rules do.
func SanitizeSystemText(text string, rules []config.ReplacePattern) string {
	paragraphs := splitParagraphs(text)
	kept := paragraphs[:0]
	for _, p := range paragraphs {
		if !dropParagraph(p, rules) {
			kept = append(kept, p)
		}
	}
	result := strings.Join(kept, "\n\n")
	for _, r := range builtinReplacements {
		result = strings.Replace(result, r.Match, r.Replacement, 1)
	}
	for _, r := range rules {
		if r.HasReplacement {
			result = strings.Replace(result, r.Match, r.Replacement, 1)
		}
	}
	return strings.TrimSpace(result)
}

func dropParagraph(p string, rules []config.ReplacePattern) bool {
	for _, s := range identityPrefixes {
		if strings.Contains(p, s) {
			return true
		}
	}
	for _, s := range removalAnchors {
		if strings.Contains(p, s) {
			return true
		}
	}
	for _, r := range rules {
		if !r.HasReplacement && strings.Contains(p, r.Match) {
			return true
		}
	}
	return false
}

// splitParagraphs splits on runs of two or more newlines.
func splitParagraphs(text string) []string {
	var out []string
	start, i := 0, 0
	for i < len(text) {
		if text[i] != '\n' || i+1 >= len(text) || text[i+1] != '\n' {
			i++
			continue
		}
		out = append(out, text[start:i])
		for i < len(text) && text[i] == '\n' {
			i++
		}
		start = i
	}
	return append(out, text[start:])
}

// withIdentity normalizes the system field to text blocks, sanitizes them and prepends the
// identity once. The identity reuses the first cache_control already present.
func withIdentity(system any, rules []config.ReplacePattern) []any {
	identity := map[string]any{"type": "text", "text": Identity}
	var blocks []any
	switch v := system.(type) {
	case nil:
		return []any{identity}
	case string:
		blocks = []any{map[string]any{"type": "text", "text": SanitizeSystemText(v, rules)}}
	case []any:
		for _, item := range v {
			blocks = append(blocks, systemBlock(item, rules))
		}
	case map[string]any:
		blocks = []any{systemBlock(v, rules)}
	default:
		return []any{identity}
	}
	if len(blocks) > 0 {
		if first, ok := blocks[0].(map[string]any); ok && first["text"] == Identity {
			return blocks
		}
	}
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if cc, ok := m["cache_control"]; ok {
				identity["cache_control"] = cc
				break
			}
		}
	}
	return append([]any{identity}, blocks...)
}

// systemBlock sanitizes a string or text block; a block without a type becomes a text block
// and keeps its metadata, such as cache_control.
func systemBlock(item any, rules []config.ReplacePattern) any {
	switch b := item.(type) {
	case string:
		return map[string]any{"type": "text", "text": SanitizeSystemText(b, rules)}
	case map[string]any:
		if t, ok := b["type"]; ok && t != "text" {
			return b
		}
		text, _ := b["text"].(string)
		b["type"] = "text"
		b["text"] = SanitizeSystemText(text, rules)
		return b
	}
	return item
}
