// Package modelsfile edits the per-model overrides of PiG's models.json in place, keeping the
// user's comments, unknown keys and layout. Only the touched values change.
package modelsfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/fsx"
)

// Op sets or removes one field of a model override. Path is relative to the override, for
// example {"contextWindow"} or {"cost", "input"}.
type Op struct {
	Path   []string
	Value  any
	Remove bool
}

// Read returns the override of provider/model as decoded JSON, or nil when there is none.
func Read(data []byte, provider, model string) (map[string]any, error) {
	plain, _, err := stripped(data)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Providers map[string]struct {
			ModelOverrides map[string]map[string]any `json:"modelOverrides"`
		} `json:"providers"`
	}
	if len(bytes.TrimSpace(plain)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, fmt.Errorf("models.json: %w", err)
	}
	return doc.Providers[provider].ModelOverrides[model], nil
}

// Apply returns data with ops applied to providers.<provider>.modelOverrides.<model>. Missing
// objects are created; objects left empty by removals are pruned back to the provider.
func Apply(data []byte, provider, model string, ops []Op) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	for _, op := range ops {
		var err error
		data, err = applyOne(data, append([]string{"providers", provider, "modelOverrides", model}, op.Path...), op)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// Update applies ops to the file at path under its lock and writes the result atomically. A
// malformed file is refused and left untouched.
func Update(ctx context.Context, path, provider, model string, ops []Op) error {
	unlock, err := fsx.Lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	data, err := fsx.ReadOptional(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	out, err := Apply(data, provider, model, ops)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fsx.WriteAtomic(path, out)
}

func applyOne(data []byte, path []string, op Op) ([]byte, error) {
	plain, trailing, err := stripped(data)
	if err != nil {
		return nil, err
	}
	root, err := parse(plain)
	if err != nil {
		return nil, err
	}
	if !root.obj {
		return nil, errors.New("top level is not an object")
	}
	// Walk as deep as the file already goes.
	chain := []*node{root}
	var found *member
	depth := 0
	for depth < len(path) {
		m := chain[len(chain)-1].find(path[depth])
		if m == nil {
			break
		}
		found = m
		depth++
		if depth < len(path) {
			if !m.val.obj {
				return nil, fmt.Errorf("%s is not an object", strings.Join(path[:depth], "."))
			}
			chain = append(chain, m.val)
		}
	}
	if depth == len(path) {
		if op.Remove {
			return remove(data, plain, chain, path, trailing)
		}
		return replace(data, found.val, op.Value)
	}
	if op.Remove {
		return data, nil
	}
	parent := chain[len(chain)-1]
	text, err := nested(path[depth:], op.Value)
	if err != nil {
		return nil, err
	}
	return insert(data, plain, parent, path[depth], text, trailing)
}

// nested renders {"a": {"b": value}} for the remaining path, without the outermost key.
func nested(path []string, value any) (string, error) {
	enc, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	out := string(enc)
	for i := len(path) - 1; i >= 1; i-- {
		k, _ := json.Marshal(path[i])
		out = "{" + string(k) + ": " + out + "}"
	}
	return out, nil
}

func replace(data []byte, v *node, value any) ([]byte, error) {
	enc, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return splice(data, v.start, v.end, enc), nil
}

func splice(data []byte, from, to int, with []byte) []byte {
	out := make([]byte, 0, len(data)-(to-from)+len(with))
	out = append(out, data[:from]...)
	out = append(out, with...)
	return append(out, data[to:]...)
}

// lineIndent is the leading whitespace of the line containing offset.
func lineIndent(data []byte, offset int) string {
	start := bytes.LastIndexByte(data[:offset], '\n') + 1
	end := start
	for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[start:end])
}

// unit is one indentation level: the file's own, else two spaces.
func unit(data []byte) string {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trim := bytes.TrimLeft(line, " \t")
		if len(trim) > 0 && len(trim) < len(line) {
			return string(line[:len(line)-len(trim)])
		}
	}
	return "  "
}

func insert(data, plain []byte, parent *node, key, text string, trailing []int) ([]byte, error) {
	u := unit(data)
	kb, _ := json.Marshal(key)
	if len(parent.members) > 0 && bytes.IndexByte(plain[parent.start:parent.end], '\n') < 0 {
		at := parent.members[len(parent.members)-1].end
		return splice(data, at, at, []byte(", "+string(kb)+": "+text)), nil
	}
	pind := lineIndent(data, parent.start)
	child := pind + u
	if len(parent.members) > 0 {
		first := parent.members[0].start
		if strings.TrimSpace(string(data[bytes.LastIndexByte(data[:first], '\n')+1:first])) == "" {
			child = lineIndent(data, first)
		}
	}
	member := string(kb) + ": " + pretty(text, child, u)
	if len(parent.members) == 0 {
		return splice(data, parent.start+1, parent.end-1, []byte("\n"+child+member+"\n"+pind)), nil
	}
	last := parent.members[len(parent.members)-1]
	after, hasComma := last.end, false
	if i := slices.IndexFunc(trailing, func(p int) bool { return p >= last.end && p < parent.end }); i >= 0 {
		after, hasComma = trailing[i]+1, true
	}
	// A comment closing the last member's line stays with that member.
	at := after
	if nl := bytes.IndexByte(plain[after:], '\n'); nl >= 0 && len(bytes.TrimSpace(plain[after:after+nl])) == 0 {
		at = after + nl
	}
	add := "\n" + child + member
	if hasComma {
		add += ","
	}
	out := splice(data, at, at, []byte(add))
	if !hasComma {
		out = splice(out, last.end, last.end, []byte(","))
	}
	return out, nil
}

// pretty indents a compact nested object one member per line.
func pretty(text, prefix, indent string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(text), prefix, indent); err != nil {
		return text
	}
	return buf.String()
}

// remove deletes the member at path and prunes the override objects it leaves empty.
func remove(data, plain []byte, chain []*node, path []string, trailing []int) ([]byte, error) {
	// Climb while the parent would be left empty; stop at the provider object.
	level := len(path) - 1
	for level > 2 && len(chain[level].members) == 1 {
		level--
	}
	parent := chain[level]
	m := parent.find(path[level])
	if m == nil {
		return data, nil
	}
	return deleteMember(data, plain, parent, m, trailing), nil
}

func deleteMember(data, plain []byte, parent *node, m *member, trailing []int) []byte {
	idx := 0
	for i := range parent.members {
		if &parent.members[i] == m {
			idx = i
		}
	}
	from, to := m.start, m.end
	// Take the member's own line when it has one to itself.
	ls := bytes.LastIndexByte(data[:from], '\n') + 1
	if strings.TrimSpace(string(data[ls:from])) == "" {
		from = ls
	}
	switch {
	case idx < len(parent.members)-1:
		// A following member keeps its own comma; drop ours.
		if c := bytes.IndexByte(plain[to:], ','); c >= 0 {
			to += c + 1
		}
	case idx > 0:
		prev := parent.members[idx-1]
		if c := bytes.IndexByte(plain[prev.end:m.start], ','); c >= 0 {
			from = prev.end + c
		}
		for _, p := range trailing {
			if p >= m.end && p < parent.end {
				to = p + 1
			}
		}
	default:
		for _, p := range trailing {
			if p >= m.end && p < parent.end {
				to = p + 1
			}
		}
	}
	if from > 0 && data[from-1] != '\n' && to < len(data) && data[to] == '\n' {
		to++
	} else if from == ls && to < len(data) && data[to] == '\n' {
		to++
	}
	out := splice(data, from, to, nil)
	return out
}
