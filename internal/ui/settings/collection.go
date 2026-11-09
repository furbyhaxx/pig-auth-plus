package settings

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/form"
)

// collection is a drill-in editor for one setting. It edits a draft and hands back a value only
// when the tab stages it; it reads no host state while rendering.
type collection interface {
	// Body renders the editor in height lines at width and reports the rows outside the window.
	Body(th line.Theme, height, width int) (lines []string, above, below int)
	// Key handles a key and reports whether it was used; keys it leaves go to the frame.
	Key(data string) bool
	// Capturing reports whether a text input owns every key, Esc included.
	Capturing() bool
	// Done reports a request to stage the draft and close, as Enter does on a one-field screen.
	Done() bool
	// Dirty reports whether the draft differs from the value it started from.
	Dirty() bool
	// Value is the draft as a normalized setting value.
	Value() any
	// Click selects the body row and reports whether it was already selected.
	Click(row int) (again bool)
	Wheel(delta int)
	Status() string
	Description() string
	Hints() []string
	Help() string
}

const addRow = "+add"

// providerList edits a list of provider ids as a checklist of known ids plus custom ones.
type providerList struct {
	known   []string
	orig    []string
	checked map[string]bool
	custom  []string
	scr     form.Screen
	in      form.Edit
	// unknown says what a custom id means here; emptyMeans explains an empty selection.
	unknown, emptyMeans string
}

func newProviderList(known, current []string, unknown, emptyMeans string) *providerList {
	p := &providerList{known: slices.Clone(known), checked: map[string]bool{}, unknown: unknown, emptyMeans: emptyMeans}
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

func (p *providerList) Dirty() bool     { return !slices.Equal(p.orig, p.Value().([]string)) }
func (p *providerList) Capturing() bool { return p.in.On }
func (p *providerList) Done() bool      { return false }

func (p *providerList) rows() []form.Row {
	var out []form.Row
	for _, id := range p.ids() {
		box := "[ ] "
		if p.checked[id] {
			box = "[x] "
		}
		r := form.Row{ID: id, Label: box + line.Plain(id)}
		if !slices.Contains(p.known, id) {
			r.Value, r.Token = "⚠ "+p.unknown, "warning"
		}
		out = append(out, r)
	}
	add := form.Row{ID: addRow, Label: "+ Custom ID", Action: true}
	if p.in.On {
		add = form.Row{ID: addRow, Label: "Custom ID"}
	}
	return append(out, add)
}

func (p *providerList) Body(th line.Theme, height, width int) ([]string, int, int) {
	edited := form.NoEdit
	if p.in.On {
		edited = string(p.in.Text)
	}
	return p.scr.Draw(th, p.rows(), edited, height, width)
}

func (p *providerList) selected() string {
	rows := p.rows()
	return rows[p.scr.Index(rows)].ID
}

func (p *providerList) Key(data string) bool {
	kb := tui.GetKeybindings()
	sel := p.selected()
	if p.in.On {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			p.in.On = false
		case kb.Matches(data, "tui.select.confirm"):
			id := p.in.Value()
			p.in.On = false
			if id != "" && !slices.Contains(p.ids(), id) {
				p.custom = append(p.custom, id)
				p.checked[id] = true
				p.scr.L.Focus(id)
			}
		default:
			p.in.Type(data)
		}
		return true
	}
	switch {
	case kb.Matches(data, "tui.select.down"):
		p.scr.L.Move(1)
	case kb.Matches(data, "tui.select.up"):
		p.scr.L.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		p.scr.L.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		p.scr.L.Page(-1)
	case sel == addRow && kb.Matches(data, "tui.select.confirm"):
		p.in.Begin("")
	case sel != addRow && (data == " " || kb.Matches(data, "tui.select.confirm")):
		p.checked[sel] = !p.checked[sel]
	case sel != addRow && !slices.Contains(p.known, sel) && (tui.MatchesKey(data, "backspace") || tui.MatchesKey(data, "delete")):
		p.custom = slices.DeleteFunc(p.custom, func(c string) bool { return c == sel })
		delete(p.checked, sel)
	default:
		return false
	}
	return true
}

func (p *providerList) Click(row int) bool { _, again := p.scr.L.Click(row); return again }
func (p *providerList) Wheel(d int)        { p.scr.L.Scroll(d) }

func (p *providerList) Status() string {
	n := len(p.Value().([]string))
	if p.Dirty() {
		return fmt.Sprintf("%d selected · ● staged", n)
	}
	return fmt.Sprintf("%d selected", n)
}

func (p *providerList) Description() string {
	if len(p.Value().([]string)) == 0 && p.emptyMeans != "" {
		return "None selected: " + p.emptyMeans + "."
	}
	return "Toggle providers or add a custom ID. Ctrl+S stages the list into Settings."
}

func (p *providerList) Hints() []string {
	if p.in.On {
		return []string{sdk.KeyHint("tui.select.confirm", "add")}
	}
	return []string{sdk.RawKeyHint("↑↓", "move"), sdk.RawKeyHint("space", "toggle"), sdk.RawKeyHint("backspace", "remove custom"), sdk.RawKeyHint("ctrl+s", "stage")}
}

func (p *providerList) Help() string {
	return ` Provider list
 space or enter toggles a provider. Enter on "+ Custom ID" adds an id that is not listed;
 backspace removes a custom id. Ctrl+S stages the list into Settings (save it there);
 Esc goes back, and asks first when the list changed.`
}

// patternList edits ordered literal rewrite rules.
type patternList struct {
	rules []config.ReplacePattern
	orig  []config.ReplacePattern
	scr   form.Screen
	in    form.Edit
	// target is the rule row being typed into: a rule id, or addRewrite/addDrop for a new one.
	target string
	// stage is the field being typed: 0 match, 1 replacement.
	stage int
	draft config.ReplacePattern
}

const (
	addRewrite = "+rewrite"
	addDrop    = "+drop"
)

func newPatternList(current []config.ReplacePattern) *patternList {
	return &patternList{rules: slices.Clone(current), orig: slices.Clone(current)}
}

func (p *patternList) Value() any      { return slices.Clone(p.rules) }
func (p *patternList) Dirty() bool     { return !slices.Equal(p.orig, p.rules) }
func (p *patternList) Capturing() bool { return p.in.On }
func (p *patternList) Done() bool      { return false }

func quote(s string) string { return `"` + strings.Join(strings.Fields(line.Plain(s)), " ") + `"` }

func ruleID(i int) string { return "r" + strconv.Itoa(i) }

func (p *patternList) rows() []form.Row {
	var out []form.Row
	for i, r := range p.rules {
		kind, value := "rewrite", quote(r.Match)+" → "+quote(r.Replacement)
		if !r.HasReplacement {
			kind, value = "drop", quote(r.Match)
		}
		row := form.Row{ID: ruleID(i), Label: fmt.Sprintf("%d %s", i+1, kind), Value: value}
		if p.in.On && p.target == row.ID {
			row.Label, row.Value = p.prompt(), ""
		}
		out = append(out, row)
	}
	rewrite := form.Row{ID: addRewrite, Label: "+ Rewrite rule", Action: true}
	drop := form.Row{ID: addDrop, Label: "+ Drop rule", Action: true}
	if p.in.On && p.target == addRewrite {
		rewrite = form.Row{ID: addRewrite, Label: p.prompt()}
	}
	if p.in.On && p.target == addDrop {
		drop = form.Row{ID: addDrop, Label: p.prompt()}
	}
	return append(out, rewrite, drop)
}

func (p *patternList) prompt() string {
	if p.stage == 0 {
		return "Match"
	}
	return "Replacement"
}

func (p *patternList) Body(th line.Theme, height, width int) ([]string, int, int) {
	edited := form.NoEdit
	if p.in.On {
		edited = string(p.in.Text)
	}
	return p.scr.Draw(th, p.rows(), edited, height, width)
}

func (p *patternList) index(id string) int {
	if !strings.HasPrefix(id, "r") || id == "r" {
		return -1
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil || n < 0 || n >= len(p.rules) {
		return -1
	}
	return n
}

func (p *patternList) begin(id string, r config.ReplacePattern) {
	p.target, p.draft, p.stage = id, r, 0
	p.in.Begin(r.Match)
}

func (p *patternList) selected() string {
	rows := p.rows()
	return rows[p.scr.Index(rows)].ID
}

func (p *patternList) Key(data string) bool {
	kb := tui.GetKeybindings()
	sel := p.selected()
	if p.in.On {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			p.in.On = false
		case kb.Matches(data, "tui.select.confirm"):
			p.advance()
		default:
			p.in.Type(data)
		}
		return true
	}
	i := p.index(sel)
	switch {
	case kb.Matches(data, "tui.select.down"):
		p.scr.L.Move(1)
	case kb.Matches(data, "tui.select.up"):
		p.scr.L.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		p.scr.L.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		p.scr.L.Page(-1)
	case kb.Matches(data, "tui.select.confirm"):
		switch {
		case sel == addRewrite:
			p.begin(sel, config.ReplacePattern{HasReplacement: true})
		case sel == addDrop:
			p.begin(sel, config.ReplacePattern{})
		case i >= 0:
			p.begin(sel, p.rules[i])
		}
	case i >= 0 && (tui.MatchesKey(data, "backspace") || tui.MatchesKey(data, "delete")):
		p.rules = slices.Delete(p.rules, i, i+1)
	case i > 0 && tui.MatchesKey(data, "shift+up"):
		p.rules[i], p.rules[i-1] = p.rules[i-1], p.rules[i]
		p.scr.L.Focus(ruleID(i - 1))
	case i >= 0 && i < len(p.rules)-1 && tui.MatchesKey(data, "shift+down"):
		p.rules[i], p.rules[i+1] = p.rules[i+1], p.rules[i]
		p.scr.L.Focus(ruleID(i + 1))
	default:
		return false
	}
	return true
}

// advance commits the field being typed: the match first, then the replacement of a rewrite.
func (p *patternList) advance() {
	if p.stage == 0 {
		match := string(p.in.Text)
		if strings.TrimSpace(match) == "" {
			return
		}
		p.draft.Match = match
		if p.draft.HasReplacement {
			p.stage = 1
			p.in.Begin(p.draft.Replacement)
			return
		}
	} else {
		repl := string(p.in.Text)
		if strings.TrimSpace(repl) == "" {
			return
		}
		p.draft.Replacement = repl
	}
	p.in.On = false
	if i := p.index(p.target); i >= 0 {
		p.rules[i] = p.draft
		return
	}
	p.rules = append(p.rules, p.draft)
	p.scr.L.Focus(ruleID(len(p.rules) - 1))
}

func (p *patternList) Click(row int) bool { _, again := p.scr.L.Click(row); return again }
func (p *patternList) Wheel(d int)        { p.scr.L.Scroll(d) }

func (p *patternList) Status() string {
	s := fmt.Sprintf("%d rules", len(p.rules))
	if len(p.rules) == 1 {
		s = "1 rule"
	}
	if p.Dirty() {
		s += " · ● staged"
	}
	return s
}

func (p *patternList) Description() string {
	if p.in.On && p.stage == 1 {
		return "The text that replaces each match in the system prompt."
	}
	if p.in.On {
		return "Literal text, not a regular expression."
	}
	return "Applied in order to Claude subscription prompts; a drop rule removes whole paragraphs."
}

func (p *patternList) Hints() []string {
	if p.in.On {
		return []string{sdk.KeyHint("tui.select.confirm", "next")}
	}
	return []string{sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "edit"), sdk.RawKeyHint("shift+↑↓", "reorder"), sdk.RawKeyHint("backspace", "delete"), sdk.RawKeyHint("ctrl+s", "stage")}
}

func (p *patternList) Help() string {
	return ` Claude system-prompt rules
 Rules are literal text, not regular expressions, applied in order to the system prompt of
 Claude subscription requests. A rewrite replaces the matching text; a drop removes every
 paragraph containing the match.
 Enter on a rule edits it, on "+ Rewrite rule" or "+ Drop rule" adds one.
 shift+up/down reorders · backspace deletes · Ctrl+S stages the rules into Settings
 (save them there); Esc goes back, and asks first when the rules changed.`
}

// templateEditor edits the quota line template as one field, with live previews at the
// widths the line is used at.
type templateEditor struct {
	orig    string
	in      form.Edit
	done    bool
	top     int
	preview func(src string, width int) (lines, unknown []string)
	rows    int
}

// previewWidths are the terminal widths the template is previewed at.
var previewWidths = []int{140, 80, 57, 36}

func newTemplateEditor(current string, preview func(string, int) ([]string, []string)) *templateEditor {
	t := &templateEditor{orig: current, preview: preview}
	t.in.Begin(current)
	return t
}

func (t *templateEditor) text() string    { return string(t.in.Text) }
func (t *templateEditor) Value() any      { return t.text() }
func (t *templateEditor) Dirty() bool     { return t.text() != t.orig }
func (t *templateEditor) Capturing() bool { return false }
func (t *templateEditor) Done() bool      { return t.done }

func (t *templateEditor) unknown() []string {
	if t.preview == nil {
		return nil
	}
	_, unknown := t.preview(t.text(), 80)
	return unknown
}

func (t *templateEditor) Body(th line.Theme, height, width int) ([]string, int, int) {
	t.rows = height
	var all []string
	all = append(all, " "+th.Paint("Template", line.Style{Bold: true}))
	// The template is edited at its end, so a long one shows its tail and leaves room for the previews.
	wrapped := tui.WrapTextWithAnsi(line.Plain(t.text())+"▏", max(width-4, 10))
	if room := max(3, height/2); len(wrapped) > room {
		all = append(all, form.Paint(th, fmt.Sprintf("   ⋯ %d earlier lines", len(wrapped)-room+1), "dim"))
		wrapped = wrapped[len(wrapped)-room+1:]
	}
	for i, l := range wrapped {
		mark := "  "
		if i == 0 {
			mark = form.Paint(th, "→ ", "accent")
		}
		all = append(all, form.Fit(mark+l, width))
	}
	if t.preview != nil {
		for _, w := range previewWidths {
			lines, _ := t.preview(t.text(), w-2)
			all = append(all, "", " "+form.Paint(th, fmt.Sprintf("Preview at %d cols", w), "muted"))
			for _, l := range lines[:min(len(lines), 3)] {
				all = append(all, form.Fit("  "+l, width))
			}
			if len(lines) > 3 {
				all = append(all, form.Paint(th, fmt.Sprintf("  … %d more rows", len(lines)-3), "dim"))
			}
		}
	}
	if u := t.unknown(); len(u) > 0 {
		all = append(all, "", form.Fit(form.Paint(th, " ⚠ ignored: "+strings.Join(u, ", "), "warning"), width))
	}
	t.top = max(0, min(t.top, len(all)-height))
	end := min(t.top+height, len(all))
	return all[t.top:end:end], t.top, len(all) - end
}

func (t *templateEditor) Key(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.confirm"):
		if strings.TrimSpace(t.text()) != "" {
			t.done = true
		}
	case kb.Matches(data, "tui.select.pageDown"):
		t.top += max(t.rows-1, 1)
	case kb.Matches(data, "tui.select.pageUp"):
		t.top = max(t.top-max(t.rows-1, 1), 0)
	default:
		t.in.Type(data)
	}
	return true
}

func (t *templateEditor) Click(int) bool { return false }
func (t *templateEditor) Wheel(d int)    { t.top = max(t.top+d, 0) }

func (t *templateEditor) Status() string {
	switch n := len(t.unknown()); {
	case n == 1:
		return "valid · 1 ignored token"
	case n > 1:
		return fmt.Sprintf("valid · %d ignored tokens", n)
	}
	return "valid"
}

func (t *templateEditor) Description() string {
	if strings.TrimSpace(t.text()) == "" {
		return "⚠ The template cannot be blank."
	}
	return "Tokens are {provider:field}; the previews update as you type."
}

func (t *templateEditor) Hints() []string {
	return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.RawKeyHint("pgup/pgdn", "page previews"), sdk.RawKeyHint("ctrl+u", "clear")}
}

func (t *templateEditor) Help() string {
	return ` Quota line template
 Literal text and {provider:field} tokens, for example {claude:id}: {claude:window-5h-used-percent}.
 Square brackets make a conditional segment; | adds styles. Unknown tokens are ignored and
 listed under the previews. The line wraps at every width, so the previews show how.
 Enter stages the template into Settings (save it there); Esc goes back, and asks first
 when the template changed. See docs/quotas.md for the full grammar.`
}
