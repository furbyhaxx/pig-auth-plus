package models

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/form"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/list"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab over a model list the owner supplies; it reads no host state while
// rendering. The catalog lists only models of signed-in providers.
type Tab struct {
	models  func() []Model
	failed  func() string
	actions Actions

	mu     sync.Mutex
	cat    list.List
	ov     *override
	al     *aliasForm
	cur    *Model // the alias model whose form is open, nil for a new alias
	notice string
	warn   bool
	above  int
	below  int
	count  int
	out    int
}

var (
	_ frame.Tab        = (*Tab)(nil)
	_ frame.Crumber    = (*Tab)(nil)
	_ frame.Statuser   = (*Tab)(nil)
	_ frame.Describer  = (*Tab)(nil)
	_ frame.Filterer   = (*Tab)(nil)
	_ frame.Overflower = (*Tab)(nil)
	_ frame.Pointer    = (*Tab)(nil)
)

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
	// RemoveAlias deletes an alias after the owner confirms it and calls [Tab.AliasSaved].
	RemoveAlias func(m Model)
}

// New builds the tab; models returns the cached catalog, nil while it is loading, and failed
// the reason the catalog could not be read, when it could not.
func New(models func() []Model, failed func() string, actions Actions) *Tab {
	return &Tab{models: models, failed: failed, actions: actions}
}

// SetNotice shows feedback in the description line until the next key.
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

// AliasSaved closes the alias form.Screen after the alias was stored or removed.
func (t *Tab) AliasSaved() {
	t.mu.Lock()
	t.al, t.cur = nil, nil
	t.mu.Unlock()
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "models" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Models" }

// Dirty implements frame.Tab.
func (t *Tab) Dirty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return (t.ov != nil && t.ov.dirty()) || (t.al != nil && t.al.dirty())
}

// Discard implements frame.Tab.
func (t *Tab) Discard() {
	t.mu.Lock()
	t.ov, t.al, t.cur, t.notice = nil, nil, nil, ""
	t.mu.Unlock()
}

// Filterable implements frame.Filterer; only the catalog takes typed filter text.
func (t *Tab) Filterable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ov == nil && t.al == nil
}

// SetFilter implements frame.Filterer.
func (t *Tab) SetFilter(q string) { t.cat.SetFilter(q) }

// Overflow implements frame.Overflower.
func (t *Tab) Overflow() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.above, t.below
}

// Crumbs implements frame.Crumber.
func (t *Tab) Crumbs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.al != nil && t.ov != nil:
		name := "New alias"
		if !t.al.isNew {
			name = "Alias " + line.Plain(t.al.orig.ID)
		}
		return []string{t.ov.model.Key(), name}
	case t.al != nil:
		return []string{line.Plain(t.al.orig.Provider + "/" + t.al.orig.ID)}
	case t.ov != nil:
		return []string{t.ov.model.Key()}
	}
	return nil
}

// Status implements frame.Statuser.
func (t *Tab) Status(frame.Env) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.al != nil:
		return t.al.status()
	case t.ov != nil:
		return t.ov.status()
	case len(t.models()) == 0 && t.failed() != "":
		return "▲ failed"
	case len(t.models()) == 0:
		return "loading…"
	case t.out > 0:
		return fmt.Sprintf("%d models · %d providers left out", t.count, t.out)
	}
	return fmt.Sprintf("%d models", t.count)
}

// signedIn is the catalog the list shows, sorted by key, and the number of providers left out.
func (t *Tab) signedIn() (shown []Model, out int) {
	left := map[string]bool{}
	for _, m := range t.models() {
		if m.SignedIn {
			shown = append(shown, m)
		} else {
			left[m.Provider] = true
		}
	}
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].Key() < shown[j].Key() })
	return shown, len(left)
}

func (t *Tab) aliasesOf(m Model) []string {
	var out []string
	for _, o := range t.models() {
		if o.Provider == m.Provider && o.AliasOf == m.ID {
			out = append(out, o.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshOpen()
	switch {
	case t.al != nil:
		lines, a, b := t.al.lines(th, env.Rows, env.Width)
		t.above, t.below = a, b
		return lines
	case t.ov != nil:
		lines, a, b := t.ov.lines(th, env.Rows, env.Width)
		t.above, t.below = a, b
		return lines
	}
	t.above, t.below = 0, 0
	if len(t.models()) == 0 {
		if msg := t.failed(); msg != "" {
			return []string{form.Fit(form.Paint(th, "  ▲ the model catalog could not be read: "+line.Plain(msg), "warning"), env.Width)}
		}
		return []string{"  reading the model catalog…"}
	}
	shown, out := t.signedIn()
	t.count, t.out = len(shown), out
	if len(shown) == 0 {
		return []string{"  no model from a signed-in provider; sign in with /login <provider>"}
	}
	entries := make([]list.Entry, len(shown))
	keyW := 0
	for i, m := range shown {
		entries[i] = list.Entry{ID: m.Key(), Search: m.Key() + " " + m.Name}
		keyW = max(keyW, tui.VisibleWidth(line.Plain(m.Key())))
	}
	win := t.cat.Window(entries, env.Rows)
	t.above, t.below = win.Above, win.Below
	if win.Total == 0 {
		return []string{"  no model matches “" + oneLine(t.cat.Query()) + "”"}
	}
	byKey := make(map[string]Model, len(shown))
	for _, m := range shown {
		byKey[m.Key()] = m
	}
	lines := make([]string, 0, len(win.Entries))
	for i, e := range win.Entries {
		lines = append(lines, row(th, byKey[e.ID], i == win.Selected, keyW, env.Width))
	}
	return lines
}

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s)
}

// refreshOpen swaps a clean open form.Screen over to the latest catalog entry, so a saved override
// shows its effect and a new alias appears in the list. Staged changes keep their entry.
func (t *Tab) refreshOpen() {
	if t.ov == nil {
		return
	}
	for _, m := range t.models() {
		if m.Key() == t.ov.model.Key() && !t.ov.dirty() {
			t.ov.model = m
		}
	}
	t.ov.aliases = t.aliasesOf(t.ov.model)
}

// Description implements frame.Describer.
func (t *Tab) Description(frame.Env) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.notice != "":
		if t.warn {
			return "▲ " + line.Plain(t.notice)
		}
		return line.Plain(t.notice)
	case t.al != nil:
		return t.al.description()
	case t.ov != nil:
		return t.ov.description()
	}
	sel := t.cat.Selected()
	shown, _ := t.signedIn()
	for _, m := range shown {
		if m.Key() != sel {
			continue
		}
		parts := []string{m.Label()}
		if m.API != "" {
			parts = append(parts, line.Plain(m.API))
		}
		if m.Reasoning {
			parts = append(parts, "reasoning")
		}
		if len(m.Input) > 0 {
			parts = append(parts, line.Plain(strings.Join(m.Input, ", ")))
		}
		parts = append(parts, "in "+Price(m.Cost.Input)+" / out "+Price(m.Cost.Output))
		return strings.Join(parts, " · ")
	}
	return "Models of signed-in providers. Enter opens one to override its fields or add an alias."
}

// open enters the selected model: an alias opens its own definition, any other model its
// detail. The caller holds t.mu.
func (t *Tab) open() {
	sel := t.cat.Selected()
	shown, _ := t.signedIn()
	for i := range shown {
		m := shown[i]
		if m.Key() != sel {
			continue
		}
		if m.AliasOf != "" {
			t.openAliasOf(m)
			return
		}
		t.ov = newOverride(m, t.aliasesOf(m), t.newAlias)
		return
	}
}

func (t *Tab) taken(m Model) func(string) bool {
	return func(id string) bool {
		for _, o := range t.models() {
			if o.Provider == m.Provider && o.ID == id {
				return true
			}
		}
		return false
	}
}

// openAliasOf opens the stored definition of alias model m.
func (t *Tab) openAliasOf(m Model) {
	if t.actions.Alias == nil {
		return
	}
	a, ok := t.actions.Alias(m)
	if !ok {
		t.notice, t.warn = "This alias is not defined in the settings and cannot be edited here.", true
		return
	}
	t.cur = &m
	t.al = newAliasForm(a, false, t.taken(m), func() {
		if t.actions.RemoveAlias != nil {
			t.actions.RemoveAlias(m)
		}
	})
}

// newAlias opens a form for a new alias of the open model. The caller holds t.mu.
func (t *Tab) newAlias() {
	if t.ov == nil {
		return
	}
	m := t.ov.model
	t.cur = nil
	t.al = newAliasForm(Alias{Provider: m.Provider, Source: m.ID}, true, t.taken(m), nil)
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notice = ""
	switch {
	case t.al != nil:
		if t.al.key(data, func(a Alias) {
			if t.actions.SaveAlias != nil {
				t.actions.SaveAlias(t.cur, a)
			}
		}) {
			t.al, t.cur = nil, nil
		}
		return true
	case t.ov != nil:
		consumed := true
		closed := t.ov.key(data, func(ops []modelsfile.Op) {
			if t.actions.SaveOverride != nil {
				t.actions.SaveOverride(t.ov.model, ops)
			}
		})
		if closed {
			t.ov = nil
		}
		if !closed && !t.ov.in.On && (tui.MatchesKey(data, "left") || tui.MatchesKey(data, "right")) {
			_, _, isField := t.ov.selected()
			consumed = isField
		}
		return consumed
	}
	switch {
	case kb.Matches(data, "tui.select.down"):
		t.cat.Move(1)
	case kb.Matches(data, "tui.select.up"):
		t.cat.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		t.cat.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		t.cat.Page(-1)
	case kb.Matches(data, "tui.select.confirm"):
		t.open()
	default:
		return false
	}
	return true
}

// Click implements frame.Pointer: a click selects, a second click opens.
func (t *Tab) Click(row, count int) bool {
	t.mu.Lock()
	l := &t.cat
	switch {
	case t.al != nil:
		l = &t.al.scr.L
	case t.ov != nil:
		l = &t.ov.scr.L
	}
	id, again := l.Click(row)
	t.mu.Unlock()
	if id == "" {
		return false
	}
	if again || count >= 2 {
		t.Key("\r")
	}
	return true
}

// Wheel implements frame.Pointer.
func (t *Tab) Wheel(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.al != nil:
		t.al.scr.L.Scroll(delta)
	case t.ov != nil:
		t.ov.scr.L.Scroll(delta)
	default:
		t.cat.Scroll(delta)
	}
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.al != nil:
		return t.al.hints()
	case t.ov != nil:
		return t.ov.hints()
	}
	return []string{sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "open")}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.al != nil:
		return strings.Split(aliasHelp, "\n")
	case t.ov != nil:
		return strings.Split(overrideHelp, "\n")
	}
	return strings.Split(helpText, "\n")
}

const helpText = ` Models
 Every model of a provider you are signed in to, with its context window on the right.
 Providers without a credential are left out; sign in with /login <provider>.
 Type to filter provider, id and name (all words must match)     Enter  open a model
 Up/Down move · PageUp/PageDown page · Esc clears the filter, then closes.
 "alias" and "override" mark models changed by pig-auth-plus.`
