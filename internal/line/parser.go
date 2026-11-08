package line

import (
	"regexp"
	"strings"
)

// Field is a provider metric or identity a token asks for.
type Field string

// Fixed fields; named window percentages are `window-<period>[-<scope>]-(used|remaining)-percent`.
const (
	FieldID               Field = "id"
	FieldName             Field = "name"
	FieldShort            Field = "short"
	FieldTier             Field = "tier"
	FieldCredits          Field = "credits"
	FieldPct              Field = "pct"
	FieldWins             Field = "wins"
	FieldRst              Field = "rst"
	FieldR                Field = "r"
	FieldBar              Field = "bar"
	FieldCreditsRemaining Field = "credits-remaining"
	FieldResetsAvailable  Field = "resets-available"
	FieldInUse            Field = "in-use"
)

var fixedFields = map[Field]bool{
	FieldID: true, FieldName: true, FieldShort: true, FieldTier: true, FieldCredits: true,
	FieldPct: true, FieldWins: true, FieldRst: true, FieldR: true, FieldBar: true,
	FieldCreditsRemaining: true, FieldResetsAvailable: true, FieldInUse: true,
}

var windowField = regexp.MustCompile(`^window-([a-z0-9]+(?:-[a-z0-9]+)*)-(used|remaining)-percent$`)

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

func isField(f Field) bool { return fixedFields[f] || windowField.MatchString(string(f)) }

// Color is a theme token or a #rgb/#rrggbb value.
type Color struct {
	Token string
	Hex   string
}

// Style is a validated token style; Stress paints the foreground by quota stress when set.
type Style struct {
	Fg, Bg                                 *Color
	Bold, Italic, Underline, Strikethrough bool
	Stress                                 *float64
}

// ThemeTokens are the theme color names a style may use.
var ThemeTokens = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`accent border borderAccent borderMuted success error warning muted dim text
		thinkingText scrollbarTrack scrollbarThumb selectedBg searchMatchBg searchMatchText userMessageBg
		userMessageText customMessageBg customMessageText customMessageLabel toolPendingBg toolSuccessBg
		toolErrorBg toolTitle toolOutput mdHeading mdLink mdLinkUrl mdCode mdCodeBlock mdCodeBlockBorder
		mdQuote mdQuoteBorder mdHr mdListBullet toolDiffAdded toolDiffRemoved toolDiffContext syntaxComment
		syntaxKeyword syntaxFunction syntaxVariable syntaxString syntaxNumber syntaxType syntaxOperator
		syntaxPunctuation thinkingOff thinkingMinimal thinkingLow thinkingMedium thinkingHigh thinkingXhigh
		thinkingMax bashMode`) {
		ThemeTokens[t] = true
	}
}

// Kind tags a template part.
type Kind int

// Template part kinds.
const (
	KindText Kind = iota
	KindSep
	KindGroup
	KindAll
	KindInUse
	KindProvider
)

// Part is a literal, break, optional group, summary or provider token.
type Part struct {
	Kind  Kind
	Text  string
	Parts []Part
	Short bool
	// Provider tokens.
	ID     string
	Alias  string
	Fields []Field
	Style  *Style
}

// Template is a parsed line template.
type Template struct {
	Parts []Part
	// Explicit is the set of PiG provider ids named by a token.
	Explicit map[string]bool
	// Unknown lists ignored malformed or unresolvable syntax.
	Unknown []string
}

func parseStyle(src string) *Style {
	st := &Style{}
	for _, raw := range strings.Split(src, ",") {
		entry := strings.TrimSpace(raw)
		switch entry {
		case "bold":
			st.Bold = true
			continue
		case "italic":
			st.Italic = true
			continue
		case "underline":
			st.Underline = true
			continue
		case "strikethrough":
			st.Strikethrough = true
			continue
		}
		slot, value, ok := strings.Cut(entry, "=")
		if !ok || (slot != "fg" && slot != "bg") {
			return nil
		}
		var c Color
		switch {
		case hexColor.MatchString(value):
			c.Hex = value
		case ThemeTokens[value]:
			c.Token = value
		default:
			return nil
		}
		if slot == "fg" {
			st.Fg = &c
		} else {
			st.Bg = &c
		}
	}
	return st
}

func parseToken(body string, resolve func(string) string) *Part {
	pieces := strings.Split(body, "|")
	if len(pieces) > 2 {
		return nil
	}
	var style *Style
	if len(pieces) == 2 {
		if style = parseStyle(pieces[1]); style == nil {
			return nil
		}
	}
	args := strings.Split(pieces[0], ":")
	if len(args) > 2 {
		return nil
	}
	alias := strings.ToLower(strings.TrimSpace(args[0]))
	var rawFields *string
	if len(args) == 2 {
		rawFields = &args[1]
	}
	switch alias {
	case "sep":
		if rawFields == nil && style == nil {
			return &Part{Kind: KindSep}
		}
		return nil
	case "all", "in-use":
		if style != nil || (rawFields != nil && *rawFields != "short") {
			return nil
		}
		k := KindAll
		if alias == "in-use" {
			k = KindInUse
		}
		return &Part{Kind: k, Short: rawFields != nil}
	}
	id := resolve(alias)
	if id == "" {
		return nil
	}
	var fields []Field
	if rawFields != nil {
		for _, f := range strings.Split(*rawFields, ",") {
			field := Field(strings.ToLower(strings.TrimSpace(f)))
			if !isField(field) {
				return nil
			}
			fields = append(fields, field)
		}
	}
	return &Part{Kind: KindProvider, ID: id, Alias: alias, Fields: fields, Style: style}
}

const maxDepth = 64

// Parse parses the quota line grammar; resolve maps a token name or alias to a PiG provider
// id, or "" when unknown. Malformed syntax is ignored and listed in Template.Unknown.
func Parse(src string, resolve func(string) string) *Template {
	t := &Template{Explicit: map[string]bool{}}
	r := []rune(src)
	i := 0
	var parts func(nested bool, depth int) []Part
	parts = func(nested bool, depth int) []Part {
		var out []Part
		text := func(s string) {
			s = Plain(s)
			if s == "" {
				return
			}
			if n := len(out); n > 0 && out[n-1].Kind == KindText {
				out[n-1].Text += s
				return
			}
			out = append(out, Part{Kind: KindText, Text: s})
		}
		for i < len(r) {
			c := r[i]
			if c == '\\' && i+1 < len(r) && strings.ContainsRune("[]{}\\", r[i+1]) {
				text(string(r[i+1]))
				i += 2
				continue
			}
			if strings.HasPrefix(string(r[i:min(i+3, len(r))]), " · ") {
				out = append(out, Part{Kind: KindSep})
				i += 3
				continue
			}
			switch c {
			case ']':
				i++
				if nested {
					return out
				}
				t.Unknown = append(t.Unknown, "]")
				continue
			case '[':
				start := i
				i++
				if depth >= maxDepth {
					t.Unknown = append(t.Unknown, string(r[start:]))
					i = len(r)
					return out
				}
				out = append(out, Part{Kind: KindGroup, Parts: parts(true, depth+1)})
				continue
			case '{':
				start := i
				i++
				closeAt, openAt := indexRune(r, '}', i), indexRune(r, '{', i)
				if closeAt < 0 || (openAt >= 0 && openAt < closeAt) {
					end := openAt
					if end < 0 {
						end = len(r)
					}
					t.Unknown = append(t.Unknown, string(r[start:end]))
					i = end
					continue
				}
				p := parseToken(string(r[i:closeAt]), resolve)
				i = closeAt + 1
				if p == nil {
					t.Unknown = append(t.Unknown, string(r[start:i]))
					continue
				}
				out = append(out, *p)
				if p.Kind == KindProvider {
					t.Explicit[p.ID] = true
				}
				continue
			case '}':
				t.Unknown = append(t.Unknown, "}")
				i++
				continue
			}
			text(string(c))
			i++
		}
		if nested {
			t.Unknown = append(t.Unknown, "[")
		}
		return out
	}
	t.Parts = parts(false, 0)
	return t
}

func indexRune(r []rune, c rune, from int) int {
	for i := from; i < len(r); i++ {
		if r[i] == c {
			return i
		}
	}
	return -1
}
