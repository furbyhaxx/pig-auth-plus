package models

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab over a model list the owner supplies; it reads no host state while
// rendering.
type Tab struct {
	models  func() []Model
	failed  func() string
	actions Actions

	mu     sync.Mutex
	query  string
	typing bool
	sel    string // key of the selected model
	top    int
	detail *Model
	ov     *override
	al     *aliasForm
	notice string
	warn   bool
	dtop   int
	dlast  int
	height int
}

var _ frame.Tab = (*Tab)(nil)

// Actions are the tab's connections to the owner.
type Actions struct {
	// SaveOverride writes the staged operations for a model; it runs off the panel's queue and
	// the owner calls [Tab.OverrideSaved] on success.
	SaveOverride func(m Model, ops []modelsfile.Op)
	// Alias returns the stored definition of an alias model.
	Alias func(m Model) (Alias, bool)
	// SaveAlias stores an alias; old is the alias being edited, nil for a new one. It runs off
	// the panel's queue and the owner calls [Tab.AliasSaved] on success.
	SaveAlias func(old *Model, a Alias)
	// RemoveAlias deletes an alias after the owner confirms it.
	RemoveAlias func(m Model)
}

// New builds the tab; models returns the cached catalog, nil while it is loading, and failed
// the reason the catalog could not be read, when it could not.
func New(models func() []Model, failed func() string, actions Actions) *Tab {
	return &Tab{models: models, failed: failed, actions: actions}
}

// SetNotice shows feedback until the next key.
func (t *Tab) SetNotice(text string, warn bool) {
	t.mu.Lock()
	t.notice, t.warn = text, warn
	t.mu.Unlock()
}

// OverrideSaved drops the staged override changes after the file was written.
func (t *Tab) OverrideSaved() {
	t.mu.Lock()
	if t.ov != nil {
		t.ov.changes, t.ov.leave = map[string]staged{}, false
	}
	t.mu.Unlock()
}

// AliasSaved closes the alias form after the alias was stored.
func (t *Tab) AliasSaved() {
	t.mu.Lock()
	t.al = nil
	t.detail = nil
	t.mu.Unlock()
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "models" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Models" }

// Dirty implements frame.Tab; browsing stages nothing.
func (t *Tab) Dirty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return (t.ov != nil && t.ov.dirty()) || (t.al != nil && t.al.dirty())
}

// Discard implements frame.Tab.
func (t *Tab) Discard() {
	t.mu.Lock()
	t.ov, t.al, t.notice = nil, nil, ""
	t.mu.Unlock()
}

// shown is the catalog filtered by the query, ordered by provider then id.
func (t *Tab) shown() []Model {
	all := t.models()
	out := make([]Model, 0, len(all))
	for _, m := range all {
		if match(m, t.query) {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func indexOf(shown []Model, key string) int {
	for i, m := range shown {
		if m.Key() == key {
			return i
		}
	}
	return 0
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.height = env.Rows
	t.refreshOpen()
	if t.ov != nil || t.al != nil {
		var lines []string
		if t.ov != nil {
			lines = t.ov.lines(th, env.Width)
		} else {
			lines = t.al.lines(th, env.Width)
		}
		return append(lines[:min(len(lines), max(env.Rows-len(t.noticeLines(th, env.Width)), 1))], t.noticeLines(th, env.Width)...)
	}
	if t.detail != nil {
		lines := detailLines(th, *t.detail, env.Width)
		lines = append(lines, t.noticeLines(th, env.Width)...)
		t.dlast = len(lines)
		t.dtop = max(0, min(t.dtop, len(lines)-env.Rows))
		end := min(t.dtop+env.Rows, len(lines))
		return lines[t.dtop:end:end]
	}
	all := t.models()
	if len(all) == 0 {
		if msg := t.failed(); msg != "" {
			return []string{fit(paint(th, " ▲ the model catalog could not be read: "+line.Plain(msg), "warning"), env.Width)}
		}
		return []string{"  reading the model catalog…"}
	}
	shown := t.shown()
	foot := []string{}
	q := " " + paint(th, "search ", "dim") + t.query
	if t.typing {
		q += "▏"
	}
	if t.query != "" || t.typing {
		foot = append(foot, fit(q, env.Width))
	}
	if len(shown) == 0 {
		return append([]string{"  no model matches"}, foot...)
	}
	sel := indexOf(shown, t.sel)
	t.sel = shown[sel].Key()
	lines, at := listLines(th, shown, sel, env.Width)
	room := max(env.Rows-len(foot), 3)
	if at[sel] < t.top {
		t.top = at[sel]
		if sel == 0 || shown[sel-1].Provider != shown[sel].Provider {
			t.top-- // keep the provider heading with its first row
		}
	} else if at[sel] >= t.top+room {
		t.top = at[sel] - room + 1
	}
	t.top = max(0, min(t.top, len(lines)-room))
	end := min(t.top+room, len(lines))
	return append(append([]string(nil), lines[t.top:end]...), foot...)
}

// refreshOpen swaps the open detail and a clean editor over to the latest catalog entry, so a
// saved override shows its effect. Staged changes keep the entry they were made against.
func (t *Tab) refreshOpen() {
	find := func(key string) (Model, bool) {
		for _, m := range t.models() {
			if m.Key() == key {
				return m, true
			}
		}
		return Model{}, false
	}
	if t.detail != nil {
		if m, ok := find(t.detail.Key()); ok {
			t.detail = &m
		}
	}
	if t.ov != nil && !t.ov.dirty() {
		if m, ok := find(t.ov.model.Key()); ok {
			t.ov.model = m
		}
	}
}

// Selected is the model under the cursor, when the list is showing.
func (t *Tab) Selected() (Model, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detail != nil {
		return *t.detail, true
	}
	shown := t.shown()
	if len(shown) == 0 {
		return Model{}, false
	}
	return shown[indexOf(shown, t.sel)], true
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notice = ""
	if t.ov != nil {
		if t.ov.key(data, func(ops []modelsfile.Op) {
			m := t.ov.model
			if t.actions.SaveOverride != nil {
				t.actions.SaveOverride(m, ops)
			}
		}) {
			t.ov = nil
		}
		return true
	}
	if t.al != nil {
		if t.al.key(data, func(a Alias) {
			var old *Model
			if !t.al.isNew {
				m := *t.detail
				old = &m
			}
			if t.actions.SaveAlias != nil {
				t.actions.SaveAlias(old, a)
			}
		}) {
			t.al = nil
		}
		return true
	}
	if t.typing {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			t.query, t.typing = "", false
		case kb.Matches(data, "tui.select.confirm"):
			t.typing = false
		case tui.MatchesKey(data, "backspace"):
			if r := []rune(t.query); len(r) > 0 {
				t.query = string(r[:len(r)-1])
			}
		default:
			for _, r := range data {
				if unicode.IsPrint(r) {
					t.query += string(r)
				}
			}
		}
		return true
	}
	if t.detail != nil {
		switch {
		case kb.Matches(data, "tui.select.cancel"), tui.MatchesKey(data, "left"):
			t.detail, t.dtop = nil, 0
		case data == "o":
			if t.detail.AliasOf != "" {
				t.notice, t.warn = "An alias takes its limits and prices from the alias editor, not from overrides.", false
			} else {
				t.ov = newOverride(*t.detail)
			}
		case data == "a":
			t.openAlias()
		case data == "d":
			if t.detail.AliasOf == "" {
				t.notice, t.warn = "Only an alias can be removed here; Backspace in the override editor removes overrides.", false
			} else if t.actions.RemoveAlias != nil {
				t.actions.RemoveAlias(*t.detail)
			}
		case kb.Matches(data, "tui.select.down"):
			t.dtop = max(0, min(t.dtop+1, t.dlast-t.height))
		case kb.Matches(data, "tui.select.up"):
			t.dtop = max(0, t.dtop-1)
		default:
			return false
		}
		return true
	}
	shown := t.shown()
	i := indexOf(shown, t.sel)
	switch {
	case data == "/":
		t.typing = true
	case kb.Matches(data, "tui.select.down"), data == "j":
		if len(shown) > 0 {
			t.sel = shown[min(i+1, len(shown)-1)].Key()
		}
	case kb.Matches(data, "tui.select.up"), data == "k":
		if len(shown) > 0 {
			t.sel = shown[max(i-1, 0)].Key()
		}
	case kb.Matches(data, "tui.select.pageDown"):
		if len(shown) > 0 {
			t.sel = shown[min(i+max(t.height-2, 1), len(shown)-1)].Key()
		}
	case kb.Matches(data, "tui.select.pageUp"):
		if len(shown) > 0 {
			t.sel = shown[max(i-max(t.height-2, 1), 0)].Key()
		}
	case kb.Matches(data, "tui.select.confirm"), tui.MatchesKey(data, "right"):
		if len(shown) > 0 {
			m := shown[i]
			t.detail, t.dtop = &m, 0
		}
	case kb.Matches(data, "tui.select.cancel") && t.query != "":
		t.query = ""
	default:
		return false
	}
	return true
}

// openAlias opens the alias form for the detail model: a new alias of it, or its own definition
// when it is an alias. The caller holds t.mu.
func (t *Tab) openAlias() {
	m := *t.detail
	taken := func(id string) bool {
		for _, o := range t.models() {
			if o.Provider == m.Provider && o.ID == id {
				return true
			}
		}
		return false
	}
	if m.AliasOf == "" {
		t.al = newAliasForm(Alias{Provider: m.Provider, Source: m.ID}, true, taken)
		return
	}
	if t.actions.Alias == nil {
		return
	}
	a, ok := t.actions.Alias(m)
	if !ok {
		t.notice, t.warn = "This alias is not defined in the settings and cannot be edited here.", true
		return
	}
	t.al = newAliasForm(a, false, taken)
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.typing:
		return []string{sdk.KeyHint("tui.select.confirm", "keep filter"), sdk.KeyHint("tui.select.cancel", "clear")}
	case t.ov != nil:
		return t.ov.hints()
	case t.al != nil:
		return t.al.hints()
	case t.detail != nil:
		if t.detail.AliasOf != "" {
			return []string{sdk.RawKeyHint("a", "edit alias"), sdk.RawKeyHint("d", "remove alias"), sdk.KeyHint("tui.select.cancel", "back")}
		}
		return []string{sdk.RawKeyHint("o", "override"), sdk.RawKeyHint("a", "new alias"), sdk.KeyHint("tui.select.cancel", "back")}
	}
	return []string{sdk.RawKeyHint("/", "search"), sdk.KeyHint("tui.select.confirm", "detail")}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ov != nil {
		return strings.Split(overrideHelp, "\n")
	}
	if t.al != nil {
		return strings.Split(aliasHelp, "\n")
	}
	return strings.Split(helpText, "\n")
}

func (t *Tab) noticeLines(th line.Theme, width int) []string {
	if t.notice == "" {
		return nil
	}
	token, glyph := "muted", "· "
	if t.warn {
		token, glyph = "warning", "▲ "
	}
	var out []string
	for i, l := range tui.WrapTextWithAnsi(line.Plain(t.notice), max(width-4, 10)) {
		p := "  "
		if i == 0 {
			p = glyph
		}
		out = append(out, " "+paint(th, p+l, token))
	}
	return out
}

const helpText = ` Models
 Every model PiG can list, grouped by provider, with its context window on the right.
 /  search provider, id and name (all words must match)     Enter  open the detail
 Up/Down or j/k  move     PageUp/PageDown  page     Esc  clear the search, then back
 alias and override tags mark models changed by pig-auth-plus.`
