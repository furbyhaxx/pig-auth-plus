package tokens

import (
	"encoding/json"
	"fmt"
	"sort"
)

// imageTokens is the host's budget of 4800 characters per image at four characters per token.
const imageTokens = 1200

// Segment is one independently countable piece of a message: text, or an image with a fixed estimate.
type Segment struct {
	Text  string
	Kind  Kind
	Image bool
}

func text(s string, k Kind) Segment { return Segment{Text: s, Kind: k} }

// MessageSegments extracts what the host's LLM conversion sends for one session message, which is
// a decoded JSON object with a "role". Roles the model never sees yield nothing.
func MessageSegments(m map[string]any) []Segment {
	switch str(m["role"]) {
	case "bashExecution":
		if m["excludeFromContext"] == true {
			return nil
		}
		return []Segment{text(bashText(m), Prose)}
	case "branchSummary":
		return []Segment{text("The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"+str(m["summary"])+"</summary>", Prose)}
	case "compactionSummary":
		return []Segment{text("The conversation history before this point was compacted into the following summary:\n\n<summary>\n"+str(m["summary"])+"\n</summary>", Prose)}
	}
	role := str(m["role"])
	var out []Segment
	switch content := m["content"].(type) {
	case string:
		out = append(out, text(content, Prose))
	case []any:
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			switch str(block["type"]) {
			case "text":
				kind := Prose
				if role == "toolResult" {
					kind = KindOf(str(block["text"]))
				}
				out = append(out, text(str(block["text"]), kind))
			case "thinking":
				out = append(out, text(str(block["thinking"]), Prose))
			case "toolCall":
				out = append(out, text(str(block["name"]), JSON), text(jsonText(block["arguments"]), JSON))
			case "image":
				out = append(out, Segment{Image: true})
			}
		}
	}
	if role == "system" {
		if sections, ok := m["sections"].(map[string]any); ok {
			names := make([]string, 0, len(sections))
			for name := range sections {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if s := str(sections[name]); s != "" {
					out = append(out, text(s, Prose))
				}
			}
		}
		if added := m["toolsAdded"]; added != nil {
			out = append(out, text(jsonText(added), JSON))
		}
	}
	return out
}

func bashText(m map[string]any) string {
	out := fmt.Sprintf("Ran `%s`\n", str(m["command"]))
	if o := str(m["output"]); o != "" {
		out += "```\n" + o + "\n```"
	} else {
		out += "(no output)"
	}
	if m["cancelled"] == true {
		out += "\n\n(command cancelled)"
	} else if code, ok := m["exitCode"].(float64); ok && code != 0 {
		out += fmt.Sprintf("\n\nCommand exited with code %d", int(code))
	}
	if m["truncated"] == true && str(m["fullOutputPath"]) != "" {
		out += "\n\n[Output truncated. Full output: " + str(m["fullOutputPath"]) + "]"
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ImageCount is the labelled estimate for one image.
func ImageCount() Count { return Count{Tokens: imageTokens, Source: SourceEstimate} }

// ToolSchema is a tool definition as the model sees it.
type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// SystemState is the prompt text and tool set the transcript's system messages currently declare.
type SystemState struct {
	Segments []Segment
	Tools    []ToolSchema
	Messages int
}

// CurrentSystem replays system messages like the host's current-system-message resolution:
// content accumulates, sections replace by name (null removes), tools add and remove by name.
// It reports false when the transcript holds no system message.
func CurrentSystem(messages []map[string]any) (SystemState, bool) {
	var st SystemState
	sections := map[string]string{}
	var sectionOrder []string
	tools := map[string]ToolSchema{}
	var toolOrder []string
	for _, m := range messages {
		if str(m["role"]) != "system" {
			continue
		}
		st.Messages++
		switch c := m["content"].(type) {
		case string:
			if c != "" {
				st.Segments = append(st.Segments, text(c, Prose))
			}
		case []any:
			for _, raw := range c {
				if b, _ := raw.(map[string]any); str(b["text"]) != "" {
					st.Segments = append(st.Segments, text(str(b["text"]), Prose))
				}
			}
		}
		if secs, ok := m["sections"].(map[string]any); ok {
			names := make([]string, 0, len(secs))
			for n := range secs {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if secs[n] == nil {
					delete(sections, n)
					continue
				}
				if _, seen := sections[n]; !seen {
					sectionOrder = append(sectionOrder, n)
				}
				sections[n] = str(secs[n])
			}
		}
		for _, raw := range anySlice(m["toolsRemoved"]) {
			delete(tools, str(asMap(raw)["name"]))
		}
		for _, raw := range anySlice(m["toolsAdded"]) {
			t := asMap(raw)
			name := str(t["name"])
			if _, seen := tools[name]; !seen {
				toolOrder = append(toolOrder, name)
			}
			tools[name] = ToolSchema{Name: name, Description: str(t["description"]), Parameters: t["parameters"]}
		}
	}
	if st.Messages == 0 {
		return SystemState{}, false
	}
	for _, n := range sectionOrder {
		if v, ok := sections[n]; ok && v != "" {
			st.Segments = append(st.Segments, text(v, Prose))
		}
	}
	for _, n := range toolOrder {
		if t, ok := tools[n]; ok {
			st.Tools = append(st.Tools, t)
		}
	}
	return st, true
}

func anySlice(v any) []any { s, _ := v.([]any); return s }
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
