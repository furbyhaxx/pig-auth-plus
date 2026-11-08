package wire

import (
	"maps"
	"slices"
	"strings"
)

// System is a transcript's current system prompt and tools, as PiG's currentSystemMessage
// and replayTools compute them.
type System struct {
	Prompt string
	Tools  []map[string]any
}

// CurrentSystem folds every system message of messages, in order. prompt is the session's
// composed prompt, used only to order sections (see OrderSections).
func CurrentSystem(messages []any, prompt string) System {
	var content []string
	var sections Sections
	var toolOrder []string
	tools := map[string]map[string]any{}
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "system" {
			continue
		}
		if text := systemText(msg["content"]); text != "" {
			content = append(content, text)
		}
		var secs Sections
		switch s := msg["sections"].(type) {
		case Sections:
			secs = s
		case map[string]any:
			secs = order(s, prompt)
		}
		for _, sec := range secs {
			i := slices.IndexFunc(sections, func(c Section) bool { return c.Name == sec.Name })
			switch {
			case sec.Value == nil && i >= 0:
				sections = slices.Delete(sections, i, i+1)
			case sec.Value == nil:
			case i >= 0:
				sections[i].Value = sec.Value
			default:
				sections = append(sections, sec)
			}
		}
		for _, r := range anySlice(msg["toolsRemoved"]) {
			name, _ := r.(map[string]any)["name"].(string)
			delete(tools, name)
			toolOrder = slices.DeleteFunc(toolOrder, func(n string) bool { return n == name })
		}
		for _, a := range anySlice(msg["toolsAdded"]) {
			tool, ok := a.(map[string]any)
			if !ok {
				continue
			}
			name, _ := tool["name"].(string)
			if _, exists := tools[name]; !exists {
				toolOrder = append(toolOrder, name)
			}
			tools[name] = maps.Clone(tool)
		}
	}
	parts := content
	if s := sections.Text(); s != "" {
		parts = append(parts, s)
	}
	out := System{Prompt: strings.Join(parts, "\n\n")}
	for _, name := range toolOrder {
		out.Tools = append(out.Tools, tools[name])
	}
	return out
}

func systemText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, b := range c {
			if block, ok := b.(map[string]any); ok && block["type"] == "text" {
				if t, _ := block["text"].(string); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}
