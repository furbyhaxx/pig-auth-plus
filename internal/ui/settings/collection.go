package settings

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// collection is a typed editor for a list setting; it edits a draft and hands back a value
// only when applied.
type collection interface {
	// Lines renders the editor within width; the owner clips to its rows.
	Lines(th line.Theme, width int) []string
	// Key handles a key and reports whether it was used.
	Key(data string) bool
	// Dirty reports whether the draft differs from the value it started from.
	Dirty() bool
	// Value is the draft as a normalized setting value.
	Value() any
	Hints() []string
	Help() string
}

// textInput is a one-line input shared by the editors.
type textInput struct {
	label string
	buf   []rune
}

func (in *textInput) key(data string) (done, cancel bool) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.cancel"):
		return false, true
	case kb.Matches(data, "tui.select.confirm"):
		return true, false
	case tui.MatchesKey(data, "backspace"):
		if n := len(in.buf); n > 0 {
			in.buf = in.buf[:n-1]
		}
	case tui.MatchesKey(data, "ctrl+u"):
		in.buf = nil
	default:
		for _, r := range data {
			if unicode.IsPrint(r) {
				in.buf = append(in.buf, r)
			}
		}
	}
	return false, false
}

func (in *textInput) line(th line.Theme, width int) string {
	return fit(" "+paint(th, in.label+" ", "accent")+string(in.buf)+"▏", width)
}

// providerList edits a list of provider ids as a checklist of known ids plus custom ones.
type providerList struct {
	known   []string
	orig    []string
	checked map[string]bool
	custom  []string
	sel     int
	input   *textInput
	// emptyMeans explains an empty selection.
	emptyMeans string
}

func newProviderList(known, current []string, emptyMeans string) *providerList {
	p := &providerList{known: slices.Clone(known), checked: map[string]bool{}, emptyMeans: emptyMeans}
	for _, id := range current {
		if !p.checked[id] {
			p.checked[id] = true
			if !slices.Contains(known, id) {
				p.custom = append(p.custom, id)
			}
		}
	}
	p.orig = p.Value().([]string)
	return p
}

func (p *providerList) ids() []string { return append(slices.Clone(p.known), p.custom...) }

// Value implements collection; ids keep the known order, then custom ones.
func (p *providerList) Value() any {
	out := []string{}
	for _, id := range p.ids() {
		if p.checked[id] {
			out = append(out, id)
		}
	}
	return out
}

// Dirty implements collection.
func (p *providerList) Dirty() bool { return !slices.Equal(p.orig, p.Value().([]string)) }

// Lines implements collection.
func (p *providerList) Lines(th line.Theme, width int) []string {
	var out []string
	for i, id := range p.ids() {
		marker, box := "  ", "[ ]"
		if i == p.sel {
			marker = paint(th, "→ ", "accent")
		}
		if p.checked[id] {
			box = "[x]"
		}
		note := ""
		if !slices.Contains(p.known, id) {
			note = paint(th, "  custom", "muted")
		}
		out = append(out, fit(marker+box+" "+line.Plain(id)+note, width))
	}
	if len(p.Value().([]string)) == 0 && p.emptyMeans != "" {
		out = append(out, fit(paint(th, " none selected: "+p.emptyMeans, "muted"), width))
	}
	if p.input != nil {
		out = append(out, p.input.line(th, width))
	}
	return out
}

// Key implements collection.
func (p *providerList) Key(data string) bool {
	if p.input != nil {
		done, cancel := p.input.key(data)
		switch {
		case cancel:
			p.input = nil
		case done:
			id := strings.TrimSpace(string(p.input.buf))
			p.input = nil
			if id != "" && !slices.Contains(p.ids(), id) {
				p.custom = append(p.custom, id)
				p.checked[id] = true
				p.sel = len(p.ids()) - 1
			}
		}
		return true
	}
	kb := tui.GetKeybindings()
	ids := p.ids()
	switch {
	case kb.Matches(data, "tui.select.down"):
		p.sel = min(p.sel+1, len(ids)-1)
	case kb.Matches(data, "tui.select.up"):
		p.sel = max(p.sel-1, 0)
	case data == " ", kb.Matches(data, "tui.select.confirm"):
		if len(ids) > 0 {
			p.checked[ids[p.sel]] = !p.checked[ids[p.sel]]
		}
	case data == "a":
		p.input = &textInput{label: "add id"}
	case data == "d":
		if len(ids) > 0 && p.sel >= len(p.known) {
			id := ids[p.sel]
			p.custom = slices.DeleteFunc(p.custom, func(c string) bool { return c == id })
			delete(p.checked, id)
			p.sel = min(p.sel, len(p.ids())-1)
		}
	default:
		return false
	}
	return true
}

// Hints implements collection.
func (p *providerList) Hints() []string {
	if p.input != nil {
		return []string{sdk.KeyHint("tui.select.confirm", "add"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{sdk.RawKeyHint("space", "toggle"), sdk.RawKeyHint("a", "add id"), sdk.RawKeyHint("d", "remove custom"), sdk.RawKeyHint("ctrl+s", "apply")}
}

// Help implements collection.
func (p *providerList) Help() string {
	return ` Provider list
 Space toggles a provider; a adds an id that is not listed, d removes a custom id.
 Ctrl+S stages the list into Settings (save it there); Esc leaves without staging.`
}

// patternList edits ordered literal rewrite rules.
type patternList struct {
	rules []config.ReplacePattern
	orig  []config.ReplacePattern
	sel   int
	input *textInput
	// stage is the field being typed: 0 match, 1 replacement; adding is a new rule.
	stage  int
	adding bool
	draft  config.ReplacePattern
}

func newPatternList(current []config.ReplacePattern) *patternList {
	return &patternList{rules: slices.Clone(current), orig: slices.Clone(current)}
}

// Value implements collection.
func (p *patternList) Value() any { return slices.Clone(p.rules) }

// Dirty implements collection.
func (p *patternList) Dirty() bool { return !slices.Equal(p.orig, p.rules) }

func describe(r config.ReplacePattern) string {
	m := `"` + strings.Join(strings.Fields(line.Plain(r.Match)), " ") + `"`
	if !r.HasReplacement {
		return "drop paragraphs containing " + m
	}
	return m + ` → "` + strings.Join(strings.Fields(line.Plain(r.Replacement)), " ") + `"`
}

// Lines implements collection.
func (p *patternList) Lines(th line.Theme, width int) []string {
	var out []string
	if len(p.rules) == 0 {
		out = append(out, " no rules; a adds one")
	}
	for i, r := range p.rules {
		marker := "  "
		if i == p.sel {
			marker = paint(th, "→ ", "accent")
		}
		out = append(out, fit(marker+fmt.Sprintf("%d. ", i+1)+describe(r), width))
	}
	if p.input != nil {
		out = append(out, p.input.line(th, width))
	}
	return out
}

func (p *patternList) startEdit(r config.ReplacePattern, adding bool) {
	p.draft, p.adding, p.stage = r, adding, 0
	p.input = &textInput{label: "match", buf: []rune(r.Match)}
}

// Key implements collection.
func (p *patternList) Key(data string) bool {
	if p.input != nil {
		done, cancel := p.input.key(data)
		switch {
		case cancel:
			p.input = nil
		case done && p.stage == 0:
			match := string(p.input.buf)
			if strings.TrimSpace(match) == "" {
				return true
			}
			p.draft.Match = match
			p.stage = 1
			p.input = &textInput{label: "replacement (empty drops the paragraph)", buf: []rune(p.draft.Replacement)}
		case done:
			repl := string(p.input.buf)
			p.draft.Replacement, p.draft.HasReplacement = repl, repl != ""
			if p.adding {
				p.rules = append(p.rules, p.draft)
				p.sel = len(p.rules) - 1
			} else {
				p.rules[p.sel] = p.draft
			}
			p.input = nil
		}
		return true
	}
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.down"):
		p.sel = min(p.sel+1, max(len(p.rules)-1, 0))
	case kb.Matches(data, "tui.select.up"):
		p.sel = max(p.sel-1, 0)
	case data == "a":
		p.startEdit(config.ReplacePattern{}, true)
	case kb.Matches(data, "tui.select.confirm"):
		if len(p.rules) > 0 {
			p.startEdit(p.rules[p.sel], false)
		}
	case data == "d":
		if len(p.rules) > 0 {
			p.rules = slices.Delete(p.rules, p.sel, p.sel+1)
			p.sel = max(min(p.sel, len(p.rules)-1), 0)
		}
	case tui.MatchesKey(data, "shift+up"):
		if p.sel > 0 {
			p.rules[p.sel], p.rules[p.sel-1] = p.rules[p.sel-1], p.rules[p.sel]
			p.sel--
		}
	case tui.MatchesKey(data, "shift+down"):
		if p.sel < len(p.rules)-1 {
			p.rules[p.sel], p.rules[p.sel+1] = p.rules[p.sel+1], p.rules[p.sel]
			p.sel++
		}
	default:
		return false
	}
	return true
}

// Hints implements collection.
func (p *patternList) Hints() []string {
	if p.input != nil {
		return []string{sdk.KeyHint("tui.select.confirm", "next"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{sdk.RawKeyHint("a", "add"), sdk.KeyHint("tui.select.confirm", "edit"), sdk.RawKeyHint("d", "delete"), sdk.RawKeyHint("shift+↑/↓", "reorder"), sdk.RawKeyHint("ctrl+s", "apply")}
}

// Help implements collection.
func (p *patternList) Help() string {
	return ` Claude system-prompt rules
 Rules are literal text, not regular expressions, applied in order to the system prompt of
 Claude subscription requests. With a replacement the matching text is rewritten; with an
 empty replacement every paragraph containing the match is dropped.
 a add · Enter edit (match, then replacement) · d delete · Shift+Up/Down reorder
 Ctrl+S stages the rules into Settings (save them there); Esc leaves without staging.`
}
