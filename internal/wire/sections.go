package wire

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// Section is one named system prompt section; a nil Value removes the section.
type Section struct {
	Name  string
	Value any
}

// Sections is a system message's sections in prompt order. The Go SDK decodes the host's
// ordered sections object into a map, losing the order; this type restores it on the way back.
type Sections []Section

// MarshalJSON writes the sections as an object in slice order.
func (s Sections) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, sec := range s {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(sec.Name)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(sec.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Text joins the non-empty section values as the host's GetCurrentSystemPrompt does.
func (s Sections) Text() string {
	var parts []string
	for _, sec := range s {
		if v, ok := sec.Value.(string); ok && v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n\n")
}

// OrderSections replaces every system message's sections map with Sections ordered by where
// each value appears in prompt, the host's composed system prompt. Sections not found in it
// follow in name order, the only order left.
func OrderSections(request map[string]any, prompt string) map[string]any {
	messages, ok := request["messages"].([]any)
	if !ok {
		return request
	}
	var out []any
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "system" {
			continue
		}
		secs, ok := msg["sections"].(map[string]any)
		if !ok {
			continue
		}
		if out == nil {
			out = slices.Clone(messages)
		}
		next := maps.Clone(msg)
		next["sections"] = order(secs, prompt)
		out[i] = next
	}
	if out == nil {
		return request
	}
	result := maps.Clone(request)
	result["messages"] = out
	return result
}

func order(secs map[string]any, prompt string) Sections {
	out := make(Sections, 0, len(secs))
	for _, name := range slices.Sorted(maps.Keys(secs)) {
		out = append(out, Section{Name: name, Value: secs[name]})
	}
	pos := func(s Section) int {
		v, ok := s.Value.(string)
		if !ok || v == "" {
			return -1
		}
		return strings.Index(prompt, v)
	}
	slices.SortStableFunc(out, func(a, b Section) int {
		pa, pb := pos(a), pos(b)
		switch {
		case pa < 0 && pb < 0:
			return 0
		case pa < 0:
			return 1
		case pb < 0:
			return -1
		}
		return pa - pb
	})
	return out
}

// SystemSections returns the ordered sections of a system message after OrderSections, or of
// a plain map when the order is unknown.
func SystemSections(msg map[string]any) Sections {
	switch s := msg["sections"].(type) {
	case Sections:
		return s
	case map[string]any:
		return order(s, "")
	}
	return nil
}
