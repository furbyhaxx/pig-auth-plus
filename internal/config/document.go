package config

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/creachadair/tomledit"
	"github.com/creachadair/tomledit/parser"
)

// maxDocumentBytes bounds the parsing of one settings document.
const maxDocumentBytes = 1 << 20

// document is one parsed settings file: validated known values plus the round-trip AST.
type document struct {
	values   map[string]any
	warnings []string
	ast      *tomledit.Document
}

// parseDocument parses and validates a settings file. Invalid known values warn and are
// skipped so the lower layer applies; syntax errors fail the whole document.
func parseDocument(path string, data []byte) (*document, error) {
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("%s exceeds 1 MiB; reduce its size", path)
	}
	tree := map[string]any{}
	if _, err := toml.Decode(string(data), &tree); err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("%s:%d: invalid TOML; fix the file before saving settings", path, perr.Position.Line)
		}
		return nil, fmt.Errorf("%s: invalid TOML; fix the file before saving settings", path)
	}
	ast, err := tomledit.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: unsupported TOML layout; use TOML 1.0 syntax", path)
	}
	doc := &document{values: map[string]any{}, ast: ast}
	var walk func(prefix string, table map[string]any)
	walk = func(prefix string, table map[string]any) {
		keys := make([]string, 0, len(table))
		for k := range table {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			value := table[k]
			if def, ok := Lookup(key); ok {
				normalized, clamped, err := def.Validate(value)
				switch {
				case err != nil:
					doc.warnings = append(doc.warnings, fmt.Sprintf("%s: %s: %v; using the inherited value", path, key, err))
				case clamped:
					doc.warnings = append(doc.warnings, fmt.Sprintf("%s: %s is outside %d–%d; clamped", path, key, def.Min, def.Max))
					doc.values[key] = normalized
				default:
					doc.values[key] = normalized
				}
				continue
			}
			if sub, ok := value.(map[string]any); ok {
				walk(key, sub)
				continue
			}
			doc.warnings = append(doc.warnings, fmt.Sprintf("%s: unknown setting %s is kept but inactive", path, key))
		}
	}
	walk("", tree)
	return doc, nil
}

// edit is one staged change of a file document; Reset removes the key.
type edit struct {
	key   string
	value any
	reset bool
}

// apply changes only the edited keys, preserving comments, order and unknown keys.
func (doc *document) apply(edits []edit) ([]byte, error) {
	for _, e := range edits {
		parts, err := parser.ParseKey(e.key)
		if err != nil {
			return nil, fmt.Errorf("invalid key %s", e.key)
		}
		entry := doc.ast.First(parts...)
		if def, _ := Lookup(e.key); e.reset && def != nil && (def.Kind == KindAliases || def.Kind == KindReplacePatterns) {
			if err := doc.setTableArray(parts, nil); err != nil {
				return nil, err
			}
			continue
		}
		if e.reset {
			if entry != nil {
				if entry.KeyValue == nil {
					return nil, fmt.Errorf("%s is a table, not a setting", e.key)
				}
				entry.Remove()
			}
			continue
		}
		if rows, ok := tableRows(e.value); ok {
			if err := doc.setTableArray(parts, rows); err != nil {
				return nil, fmt.Errorf("%s: %w", e.key, err)
			}
			continue
		}
		text, err := encodeValue(e.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
		value, err := parser.ParseValue(text)
		if err != nil {
			return nil, fmt.Errorf("%s: cannot encode value", e.key)
		}
		if entry != nil {
			if entry.KeyValue == nil {
				return nil, fmt.Errorf("%s is a table, not a setting", e.key)
			}
			entry.Value.X = value.X
			continue
		}
		if inline := doc.inlineParent(parts); inline != nil {
			inline.Value.X = append(inline.Value.X.(parser.Inline), &parser.KeyValue{Name: parts[len(parts)-1:], Value: value})
			continue
		}
		section := doc.ast.Global
		if len(parts) > 1 {
			section = doc.section(parser.Key(parts[:len(parts)-1]))
		}
		section.Items = append(section.Items, &parser.KeyValue{Name: parser.Key{parts[len(parts)-1]}, Value: value})
	}
	var out bytes.Buffer
	if err := tomledit.Format(&out, doc.ast); err != nil {
		return nil, errors.New("settings edit failed; the file is unchanged")
	}
	return out.Bytes(), nil
}

// inlineParent finds `parent = { … }` written as an inline table holding the key.
func (doc *document) inlineParent(parts parser.Key) *tomledit.Entry {
	if len(parts) < 2 {
		return nil
	}
	parent := doc.ast.First(parts[:len(parts)-1]...)
	if parent == nil || parent.KeyValue == nil {
		return nil
	}
	if _, ok := parent.Value.X.(parser.Inline); ok {
		return parent
	}
	return nil
}

func (doc *document) section(name parser.Key) *tomledit.Section {
	for _, s := range doc.ast.Sections {
		if s.TableName().Equals(name) {
			return s
		}
	}
	s := &tomledit.Section{Heading: &parser.Heading{Name: name}}
	doc.ast.Sections = append(doc.ast.Sections, s)
	return s
}

// encodeValue renders a normalized setting value as TOML value text.
func encodeValue(v any) (string, error) {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case string:
		return quote(x), nil
	case []string:
		items := make([]string, len(x))
		for i, s := range x {
			items[i] = quote(s)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	}
	return "", fmt.Errorf("cannot encode %T", v)
}

// field is one encoded key of a table-array row.
type field struct{ name, value string }

// tableRows renders table-list settings as rows of encoded fields; ok is false for others.
func tableRows(v any) (rows [][]field, ok bool) {
	switch x := v.(type) {
	case []Alias:
		for _, a := range x {
			row := []field{{"provider", quote(a.Provider)}, {"source", quote(a.Source)}, {"id", quote(a.ID)}}
			if a.Name != "" {
				row = append(row, field{"name", quote(a.Name)})
			}
			if a.ContextWindow > 0 {
				row = append(row, field{"contextWindow", strconv.FormatInt(a.ContextWindow, 10)})
			}
			if a.MaxTokens > 0 {
				row = append(row, field{"maxTokens", strconv.FormatInt(a.MaxTokens, 10)})
			}
			rows = append(rows, row)
		}
		return rows, true
	case []ReplacePattern:
		for _, p := range x {
			row := []field{{"match", quote(p.Match)}}
			if p.HasReplacement {
				row = append(row, field{"replacement", quote(p.Replacement)})
			}
			rows = append(rows, row)
		}
		return rows, true
	}
	return nil, false
}

// setTableArray replaces every definition of key (assignment or [[key]] tables) with one
// [[key]] table per row, placed where the first old table was.
func (doc *document) setTableArray(parts parser.Key, rows [][]field) error {
	if entry := doc.ast.First(parts...); entry != nil && entry.KeyValue != nil {
		entry.Remove()
	}
	at := len(doc.ast.Sections)
	kept := doc.ast.Sections[:0]
	for _, s := range doc.ast.Sections {
		if s.Heading != nil && s.Heading.IsArray && s.TableName().Equals(parts) {
			at = min(at, len(kept))
			continue
		}
		kept = append(kept, s)
	}
	at = min(at, len(kept))
	added := make([]*tomledit.Section, 0, len(rows))
	for _, row := range rows {
		s := &tomledit.Section{Heading: &parser.Heading{IsArray: true, Name: parts}}
		for _, f := range row {
			value, err := parser.ParseValue(f.value)
			if err != nil {
				return fmt.Errorf("cannot encode %s", f.name)
			}
			s.Items = append(s.Items, &parser.KeyValue{Name: parser.Key{f.name}, Value: value})
		}
		added = append(added, s)
	}
	doc.ast.Sections = append(kept[:at:at], append(added, kept[at:]...)...)
	return nil
}

// quote renders a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
