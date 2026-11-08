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
}

// Tab implements frame.Tab over a settings snapshot and staged edits.
type Tab struct {
	snap    func() *config.Snapshot
	actions Actions

	mu      sync.Mutex
	sel     int
	top     int
	staged  map[string]config.Edit
	editing *edit
	coll    collection
	collKey string
	leave   bool
	notice  string
	warn    bool
}

type edit struct {
	key string
	buf []rune
}

var _ frame.Tab = (*Tab)(nil)

// New builds the tab; selectKey starts on that setting when it names one.
func New(snap func() *config.Snapshot, actions Actions, selectKey string) *Tab {
	t := &Tab{snap: snap, actions: actions, staged: map[string]config.Edit{}}
	for i, d := range config.Catalog {
		if d.Key == selectKey {
			t.sel = i
		}
	}
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

// SetNotice shows feedback until the next key.
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

func (t *Tab) rows() []row {
	snap := t.snap()
	out := make([]row, len(config.Catalog))
	for i := range config.Catalog {
		d := &config.Catalog[i]
		r := row{def: d, value: snap.Value(d.Key), source: snap.Source(d.Key).String()}
		if e, ok := t.staged[d.Key]; ok {
			r.staged, r.reset = true, e.Reset
			r.value = e.Value
			if e.Reset {
				r.value = d.Default
			}
		}
		out[i] = r
	}
	return out
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.coll != nil {
		return t.collBody(th, env)
	}
	rows := t.rows()
	t.sel = max(0, min(t.sel, len(rows)-1))
	cur := rows[t.sel]

	var foot []string
	if t.editing != nil {
		foot = append(foot, fit(paint(th, " edit ", "accent")+string(t.editing.buf)+"▏", env.Width))
	}
	for _, l := range tui.WrapTextWithAnsi(paint(th, cur.def.Help, "muted"), max(env.Width-2, 10)) {
		foot = append(foot, " "+l)
	}
	if cur.def.Key == "quotas.lineTemplate" && t.actions.Preview != nil {
		src := fmt.Sprint(cur.value)
		if t.editing != nil && t.editing.key == cur.def.Key {
			src = string(t.editing.buf)
		}
		lines, unknown := t.actions.Preview(src, env.Width-2)
		foot = append(foot, paint(th, " preview", "dim"))
		for _, l := range lines[:min(len(lines), 3)] {
			foot = append(foot, " "+fit(l, env.Width-1))
		}
		if len(unknown) > 0 {
			foot = append(foot, fit(paint(th, " ▲ ignored: "+strings.Join(unknown, ", "), "warning"), env.Width))
		}
	}
	if t.notice != "" {
		token, glyph := "muted", "· "
		if t.warn {
			token, glyph = "warning", "▲ "
		}
		for i, l := range tui.WrapTextWithAnsi(t.notice, max(env.Width-4, 10)) {
			p := "  "
			if i == 0 {
				p = glyph
			}
			foot = append(foot, " "+paint(th, p+l, token))
		}
	}
	room := max(env.Rows-len(foot), 3)

	var lines []string
	spans := make([][2]int, len(rows))
	for i, r := range rows {
		l := rowLines(th, r, i == t.sel, env.Width)
		spans[i] = [2]int{len(lines), len(lines) + len(l)}
		lines = append(lines, l...)
	}
	if s := spans[t.sel]; s[0] < t.top {
		t.top = s[0]
	} else if s[1] > t.top+room {
		t.top = s[1] - room
	}
	t.top = max(0, min(t.top, len(lines)-room))
	end := min(t.top+room, len(lines))
	body := append([]string(nil), lines[t.top:end]...)
	return append(body, foot...)
}

var printable = func(r rune) bool { return unicode.IsPrint(r) }

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	t.mu.Lock()
	t.notice = ""
	if t.coll != nil {
		defer t.mu.Unlock()
		return t.collKeyLocked(data)
	}
	if t.editing != nil {
		defer t.mu.Unlock()
		return t.editKey(data)
	}
	kb := tui.GetKeybindings()
	var save []config.Edit
	switch {
	case kb.Matches(data, "tui.select.down"), data == "j":
		t.sel = min(t.sel+1, len(config.Catalog)-1)
	case kb.Matches(data, "tui.select.up"), data == "k":
		t.sel = max(t.sel-1, 0)
	case tui.MatchesKey(data, "left"), data == "h":
		t.step(-1)
	case tui.MatchesKey(data, "right"), data == "l":
		t.step(1)
	case data == " ":
		t.step(1)
	case kb.Matches(data, "tui.select.confirm"):
		t.enter()
	case tui.MatchesKey(data, "backspace"), tui.MatchesKey(data, "delete"):
		d := &config.Catalog[t.sel]
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
	t.mu.Unlock()
	if save != nil && t.actions.Save != nil {
		t.actions.Save(save)
	}
	return true
}

func (t *Tab) current(d *config.Definition) any {
	if e, ok := t.staged[d.Key]; ok {
		if e.Reset {
			return d.Default
		}
		return e.Value
	}
	return t.snap().Value(d.Key)
}

func (t *Tab) stage(d *config.Definition, v any) {
	norm, _, err := d.Validate(v)
	if err != nil {
		t.notice, t.warn = d.Key+": "+err.Error(), true
		return
	}
	t.staged[d.Key] = config.Edit{Key: d.Key, Value: norm}
}

// step changes the selected setting by one: toggles, cycles or nudges.
func (t *Tab) step(dir int) {
	d := &config.Catalog[t.sel]
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
func (t *Tab) enter() {
	d := &config.Catalog[t.sel]
	switch d.Kind {
	case config.KindBool, config.KindEnum:
		t.step(1)
	case config.KindStringList:
		var known []string
		if t.actions.Known != nil {
			known = t.actions.Known(d.Key)
		}
		empty := ""
		if d.Key == "quotas.providers" {
			empty = "all quota sources are polled"
		} else {
			empty = "the override is disabled"
		}
		t.coll, t.collKey, t.leave = newProviderList(known, t.current(d).([]string), empty), d.Key, false
	case config.KindInt, config.KindString:
		cur := t.current(d)
		text := ""
		switch x := cur.(type) {
		case int64:
			text = strconv.FormatInt(x, 10)
		case string:
			text = x
		}
		t.editing = &edit{key: d.Key, buf: []rune(text)}
	case config.KindAliases:
		t.notice, t.warn = "Aliases are edited in the Models tab.", false
	case config.KindReplacePatterns:
		t.coll, t.collKey, t.leave = newPatternList(t.current(d).([]config.ReplacePattern)), d.Key, false
	}
}

// editKey handles the inline editor; the caller holds t.mu.
func (t *Tab) editKey(data string) bool {
	e := t.editing
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.cancel"):
		t.editing = nil
	case kb.Matches(data, "tui.select.confirm"):
		t.commit()
	case tui.MatchesKey(data, "backspace"):
		if n := len(e.buf); n > 0 {
			e.buf = e.buf[:n-1]
		}
	case tui.MatchesKey(data, "ctrl+u"):
		e.buf = nil
	default:
		for _, r := range data {
			if printable(r) {
				e.buf = append(e.buf, r)
			}
		}
	}
	return true
}

func (t *Tab) commit() {
	e := t.editing
	d, _ := config.Lookup(e.key)
	text := string(e.buf)
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
		if t.inputOpenOn(coll) {
			return coll.Hints()
		}
		return append(coll.Hints(), sdk.KeyHint("tui.select.cancel", "back"))
	}
	if editing {
		return []string{sdk.KeyHint("tui.select.confirm", "stage"), sdk.KeyHint("tui.select.cancel", "cancel")}
	}
	return []string{
		sdk.RawKeyHint("←/→", "change"),
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
 Left/Right or h/l  toggle, cycle or nudge      Enter  edit text, numbers and lists
 Space  toggle or advance                      Backspace  stage the built-in default
 Ctrl+S  save staged edits                      Esc  cancel an edit; closing asks first
 Lists open a typed editor. A higher layer overrides a lower one, so a saved
 value can stay shadowed; the save reports which keys.`

func (t *Tab) collBody(th line.Theme, env frame.Env) []string {
	lines := t.coll.Lines(th, env.Width)
	head := paint(th, " Settings › "+t.collKey, "accent")
	if t.coll.Dirty() {
		head += paint(th, "  ● unsaved", "warning")
	}
	out := []string{fit(head, env.Width)}
	out = append(out, lines[:min(len(lines), max(env.Rows-2, 1))]...)
	if t.leave {
		out = append(out, fit(paint(th, " ▲ Esc again discards this draft; ctrl+s applies it", "warning"), env.Width))
	}
	return out
}

// collKeyLocked routes a key to the open collection editor; the caller holds t.mu.
func (t *Tab) collKeyLocked(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case tui.MatchesKey(data, "ctrl+s"):
		d, _ := config.Lookup(t.collKey)
		t.stage(d, t.coll.Value())
		t.coll, t.leave = nil, false
		return true
	case kb.Matches(data, "tui.select.cancel") && !t.inputOpen():
		if t.coll.Dirty() && !t.leave {
			t.leave = true
			return true
		}
		t.coll, t.leave = nil, false
		return true
	}
	t.leave = false
	t.coll.Key(data)
	return true
}

// inputOpen reports whether the editor's inline input owns Esc.
func (t *Tab) inputOpen() bool { return t.inputOpenOn(t.coll) }

func (t *Tab) inputOpenOn(coll collection) bool {
	switch c := coll.(type) {
	case *providerList:
		return c.input != nil
	case *patternList:
		return c.input != nil
	}
	return false
}
