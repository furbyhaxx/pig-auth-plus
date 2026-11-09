package settings

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/form"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Actions are the tab's connections to the owner.
type Actions struct {
	// Save asks the owner to pick a scope and persist the staged edits; the owner calls
	// [Tab.Saved] on success. It runs off the panel's queue.
	Save func(edits []config.Edit)
	// Known lists the provider ids offered by a provider-list setting.
	Known func(key string) []string
	// Preview renders a quota line template at width and lists its ignored syntax.
	Preview func(template string, width int) (lines, unknown []string)
	// Goto opens another dashboard tab by id.
	Goto func(tab string)
}

// Tab implements frame.Tab over a settings snapshot and staged edits.
type Tab struct {
	snap    func() *config.Snapshot
	actions Actions

	mu      sync.Mutex
	scr     form.Screen
	staged  map[string]config.Edit
	editing *form.Edit
	editKey string
	coll    collection
	collKey string
	leave   bool
	jump    string
	notice  string
	warn    bool
	above   int
	below   int
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

// New builds the tab; selectKey starts on that setting when it names one.
func New(snap func() *config.Snapshot, actions Actions, selectKey string) *Tab {
	t := &Tab{snap: snap, actions: actions, staged: map[string]config.Edit{}}
	t.scr.L.Window(form.Entries(t.rows()), 1<<10)
	t.scr.L.Select(selectKey)
	return t
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "settings" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Settings" }

// Dirty implements frame.Tab.
func (t *Tab) Dirty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.staged) > 0 || t.editing != nil || (t.coll != nil && t.coll.Dirty())
}

// Discard implements frame.Tab.
func (t *Tab) Discard() {
	t.mu.Lock()
	t.staged, t.editing, t.coll, t.leave, t.notice = map[string]config.Edit{}, nil, nil, false, ""
	t.mu.Unlock()
}

// SetNotice shows feedback in the description line until the next key.
func (t *Tab) SetNotice(text string, warn bool) {
	t.mu.Lock()
	t.notice, t.warn = text, warn
	t.mu.Unlock()
}

// Saved drops the staged edits that were written.
func (t *Tab) Saved(keys []string) {
	t.mu.Lock()
	for _, k := range keys {
		delete(t.staged, k)
	}
	t.mu.Unlock()
}

// Staged returns the staged edits in catalog order.
func (t *Tab) Staged() []config.Edit {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stagedLocked()
}

func (t *Tab) stagedLocked() []config.Edit {
	var out []config.Edit
	for _, d := range config.Catalog {
		if e, ok := t.staged[d.Key]; ok {
			out = append(out, e)
		}
	}
	return out
}

// Filterable implements frame.Filterer; only the setting list takes typed filter text.
func (t *Tab) Filterable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.coll == nil && t.editing == nil
}

// SetFilter implements frame.Filterer.
func (t *Tab) SetFilter(q string) { t.scr.L.SetFilter(q) }

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
	if t.coll != nil {
		label, _ := labelOf(t.collKey)
		return []string{label}
	}
	return nil
}

// Status implements frame.Statuser.
func (t *Tab) Status(frame.Env) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.coll != nil:
		return t.coll.Status()
	case len(t.staged) > 0:
		return fmt.Sprintf("%d settings · ● %d staged", len(config.Catalog), len(t.staged))
	}
	return fmt.Sprintf("%d settings", len(config.Catalog))
}

var sections = map[string]string{
	"dashboard": "Dashboard", "quotas": "Quotas", "models": "Models",
	"openai": "OpenAI", "claude": "Claude", "tokens": "Tokens",
}

// units are the suffixes of a key that name the unit of its number.
var units = []struct{ suffix, unit string }{{"Sec", " s"}, {"Percent", "%"}, {"Usd", " USD"}, {"Cols", " cols"}}

// labelOf turns "refreshIntervalSec" into "Refresh interval" and its unit " s".
func labelOf(key string) (label, unit string) {
	name := key[strings.IndexByte(key, '.')+1:]
	for _, u := range units {
		if strings.HasSuffix(name, u.suffix) && len(name) > len(u.suffix) {
			name, unit = strings.TrimSuffix(name, u.suffix), u.unit
		}
	}
	var b strings.Builder
	for i, r := range name {
		switch {
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsUpper(r):
			b.WriteByte(' ')
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), unit
}

// display is a value as a row shows it: [x]/[ ] toggles, ‹ value › cycles, units on numbers.
func display(d *config.Definition, v any) string {
	_, unit := labelOf(d.Key)
	switch x := v.(type) {
	case bool:
		if x {
			return "[x] on"
		}
		return "[ ] off"
	case string:
		if d.Kind == config.KindEnum {
			return "‹ " + x + " ›"
		}
	case int64:
		return fmt.Sprint(x) + unit
	case []string:
		if len(x) == 0 && d.Key == "quotas.providers" {
			return "all sources"
		}
	case []config.Alias:
		return fmt.Sprintf("%d alias(es)", len(x))
	case []config.ReplacePattern:
		return fmt.Sprintf("%d rule(s)", len(x))
	}
	return Format(d, v)
}

func (t *Tab) value(d *config.Definition) (v any, staged, reset bool) {
	if e, ok := t.staged[d.Key]; ok {
		if e.Reset {
			return d.Default, true, true
		}
		return e.Value, true, false
	}
	return t.snap().Value(d.Key), false, false
}

func (t *Tab) rows() []form.Row {
	snap := t.snap()
	var out []form.Row
	last := ""
	for i := range config.Catalog {
		d := &config.Catalog[i]
		section := d.Key[:strings.IndexByte(d.Key, '.')]
		if section != last {
			title := sections[section]
			if title == "" {
				title = section
			}
			out = append(out, form.Row{ID: "§" + section, Label: title, Header: true})
			last = section
		}
		label, _ := labelOf(d.Key)
		v, staged, reset := t.value(d)
		r := form.Row{ID: d.Key, Label: label, Value: display(d, v), Search: d.Key, Source: snap.Source(d.Key).String(), Token: "muted"}
		if staged {
			r.Source, r.Token = "● staged", "warning"
			if reset {
				r.Source = "● staged reset"
			}
		}
		out = append(out, r)
	}
	return out
}

// selected is the definition under the cursor, nil when the filter matches nothing.
func (t *Tab) selected() *config.Definition {
	d, _ := config.Lookup(t.scr.L.Selected())
	return d
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.coll != nil {
		lines, above, below := t.coll.Body(th, env.Rows, env.Width)
		t.above, t.below = above, below
		return lines
	}
	edited := form.NoEdit
	if t.editing != nil {
		edited = string(t.editing.Text)
	}
	lines, above, below := t.scr.Draw(th, t.rows(), edited, env.Rows, env.Width)
	t.above, t.below = above, below
	if len(lines) == 0 {
		return []string{"  no setting matches “" + line.Plain(t.scr.L.Query()) + "”"}
	}
	return lines
}

// Description implements frame.Describer.
func (t *Tab) Description(env frame.Env) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.notice != "":
		if t.warn {
			return "▲ " + line.Plain(t.notice)
		}
		return line.Plain(t.notice)
	case t.coll != nil && t.leave:
		return "▲ Esc again discards these changes; ctrl+s stages them."
	case t.coll != nil:
		return t.coll.Description()
	}
	d := t.selected()
	if d == nil {
		return "Type to filter settings by name or value."
	}
	if d.Key == "quotas.lineTemplate" && t.actions.Preview != nil {
		src := fmt.Sprint(t.snap().Value(d.Key))
		if t.editing != nil {
			src = string(t.editing.Text)
		}
		if lines, _ := t.actions.Preview(src, max(env.Width-10, 10)); len(lines) > 0 {
			return "preview " + line.Plain(lines[0])
		}
	}
	return d.Key + " · " + d.Help
}

// Click implements frame.Pointer: a click selects, a second click opens.
func (t *Tab) Click(row, count int) bool {
	t.mu.Lock()
	if t.coll != nil {
		again := t.coll.Click(row)
		t.mu.Unlock()
		if again || count >= 2 {
			t.Key("\r")
		}
		return true
	}
	if t.editing != nil {
		t.mu.Unlock()
		return false
	}
	id, again := t.scr.L.Click(row)
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
	if t.coll != nil {
		t.coll.Wheel(delta)
		return
	}
	t.scr.L.Scroll(delta)
}

var printable = func(r rune) bool { return unicode.IsPrint(r) }

// steppable reports whether Left/Right change the setting in place.
func steppable(d *config.Definition) bool {
	return d.Kind == config.KindBool || d.Kind == config.KindEnum || d.Kind == config.KindInt
}

// Key implements frame.Tab. Bare letters are never bound: on the list they feed the filter.
func (t *Tab) Key(data string) bool {
	t.mu.Lock()
	t.notice = ""
	if t.coll != nil {
		defer t.mu.Unlock()
		return t.collKeyLocked(data)
	}
	if t.editing != nil {
		defer t.mu.Unlock()
		return t.editKeyLocked(data)
	}
	kb := tui.GetKeybindings()
	d := t.selected()
	idle := t.scr.L.Query() == ""
	var save []config.Edit
	t.jump = ""
	switch {
	case kb.Matches(data, "tui.select.down"):
		t.scr.L.Move(1)
	case kb.Matches(data, "tui.select.up"):
		t.scr.L.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		t.scr.L.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		t.scr.L.Page(-1)
	case (tui.MatchesKey(data, "left") || tui.MatchesKey(data, "right")) && d != nil && steppable(d):
		dir := 1
		if tui.MatchesKey(data, "left") {
			dir = -1
		}
		t.step(d, dir)
	case data == " " && idle && d != nil:
		t.step(d, 1)
	case kb.Matches(data, "tui.select.confirm") && d != nil:
		t.enter(d)
	case (tui.MatchesKey(data, "backspace") || tui.MatchesKey(data, "delete")) && idle && d != nil:
		t.staged[d.Key] = config.Edit{Key: d.Key, Reset: true}
	case tui.MatchesKey(data, "ctrl+s"):
		if len(t.staged) == 0 {
			t.notice, t.warn = "Nothing staged to save.", false
		} else {
			save = t.stagedLocked()
		}
	default:
		t.mu.Unlock()
		return false
	}
	jump := t.jump
	t.mu.Unlock()
	if save != nil && t.actions.Save != nil {
		t.actions.Save(save)
	}
	if jump != "" && t.actions.Goto != nil {
		t.actions.Goto(jump)
	}
	return true
}

func (t *Tab) current(d *config.Definition) any {
	v, _, _ := t.value(d)
	return v
}

func (t *Tab) stage(d *config.Definition, v any) {
	norm, _, err := d.Validate(v)
	if err != nil {
		t.notice, t.warn = d.Key+": "+err.Error(), true
		return
	}
	t.staged[d.Key] = config.Edit{Key: d.Key, Value: norm}
}

// step changes the setting by one: toggles, cycles or nudges.
func (t *Tab) step(d *config.Definition, dir int) {
	switch d.Kind {
	case config.KindBool:
		t.stage(d, dir > 0)
	case config.KindEnum:
		cur, _ := t.current(d).(string)
		i := 0
		for j, e := range d.Enum {
			if e == cur {
				i = j
			}
		}
		t.stage(d, d.Enum[(i+dir+len(d.Enum))%len(d.Enum)])
	case config.KindInt:
		n, _ := t.current(d).(int64)
		span := d.Max - d.Min
		unit := int64(1)
		switch {
		case span > 600:
			unit = 15
		case span > 100:
			unit = 5
		}
		t.stage(d, n+int64(dir)*unit)
	default:
		t.notice, t.warn = d.Key+" is edited with Enter.", false
	}
}

// enter opens the inline editor, toggles or cycles, or opens a collection editor.
func (t *Tab) enter(d *config.Definition) {
	switch d.Kind {
	case config.KindBool, config.KindEnum:
		t.step(d, 1)
	case config.KindStringList:
		var known []string
		if t.actions.Known != nil {
			known = t.actions.Known(d.Key)
		}
		unknown, empty := "custom id", "the override is disabled"
		if d.Key == "quotas.providers" {
			unknown, empty = "unknown id · ignored", "all quota sources are polled"
		}
		t.coll, t.collKey, t.leave = newProviderList(known, t.current(d).([]string), unknown, empty), d.Key, false
		t.scr.L.SetFilter("")
	case config.KindString:
		t.coll, t.collKey, t.leave = newTemplateEditor(t.current(d).(string), t.actions.Preview), d.Key, false
	case config.KindInt:
		text := ""
		switch x := t.current(d).(type) {
		case int64:
			text = strconv.FormatInt(x, 10)
		case string:
			text = x
		}
		t.editing, t.editKey = &form.Edit{}, d.Key
		t.editing.Begin(text)
	case config.KindAliases:
		t.jump = "models"
	case config.KindReplacePatterns:
		t.coll, t.collKey, t.leave = newPatternList(t.current(d).([]config.ReplacePattern)), d.Key, false
	}
	if t.coll != nil {
		t.scr.L.SetFilter("")
	}
}

// editKeyLocked handles the inline editor; the caller holds t.mu.
func (t *Tab) editKeyLocked(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.cancel"):
		t.editing = nil
	case kb.Matches(data, "tui.select.confirm"):
		t.commit()
	default:
		t.editing.Type(data)
	}
	return true
}

func (t *Tab) commit() {
	d, _ := config.Lookup(t.editKey)
	text := string(t.editing.Text)
	var value any
	switch d.Kind {
	case config.KindInt:
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			t.notice, t.warn = d.Key+": expected an integer", true
			return
		}
		value = n
	default:
		value = text
	}
	norm, clamped, err := d.Validate(value)
	if err != nil {
		t.notice, t.warn = d.Key+": "+err.Error(), true
		return
	}
	t.staged[d.Key] = config.Edit{Key: d.Key, Value: norm}
	t.editing = nil
	if clamped {
		t.notice, t.warn = fmt.Sprintf("%s was clamped to %v.", d.Key, norm), true
	}
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	editing := t.editing != nil
	coll := t.coll
	t.mu.Unlock()
	if coll != nil {
		return coll.Hints()
	}
	if editing {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{
		sdk.RawKeyHint("←→", "change"),
		sdk.KeyHint("tui.select.confirm", "edit"),
		sdk.RawKeyHint("ctrl+s", "save"),
		sdk.RawKeyHint("backspace", "default"),
	}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string {
	t.mu.Lock()
	coll := t.coll
	t.mu.Unlock()
	if coll != nil {
		return strings.Split(coll.Help(), "\n")
	}
	return strings.Split(helpText, "\n")
}

const helpText = ` Settings
 Every setting with its effective value and the layer it comes from:
 default, user (auth-plus.toml) or session. Edits are staged, shown with ●, and written
 only when you save; the save asks for user or session scope.
 type  filter by name or value             ←→ or space  toggle, cycle or nudge
 enter  edit text, numbers and lists        backspace  stage the built-in default
 ctrl+s  save staged edits                  esc  clear the filter, cancel an edit, or close
 Lists open a typed editor. A higher layer overrides a lower one, so a saved
 value can stay shadowed; the save reports which keys.`

// collKeyLocked routes a key to the open drill-in editor; the caller holds t.mu.
func (t *Tab) collKeyLocked(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case tui.MatchesKey(data, "ctrl+s"):
		t.stageCollection()
		return true
	case kb.Matches(data, "tui.select.cancel") && !t.coll.Capturing():
		if t.coll.Dirty() && !t.leave {
			t.leave = true
			return true
		}
		t.coll, t.leave = nil, false
		return true
	}
	t.leave = false
	used := t.coll.Key(data)
	if t.coll.Done() {
		t.stageCollection()
	}
	return used
}

func (t *Tab) stageCollection() {
	if t.coll.Dirty() {
		d, _ := config.Lookup(t.collKey)
		t.stage(d, t.coll.Value())
	}
	t.coll, t.leave = nil, false
}
