package providers

import (
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/list"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Actions are what the tab asks of its owner.
type Actions struct {
	// Reread refreshes credential state in the background.
	Reread func()
	// Goto opens another dashboard tab by id.
	Goto func(tab string)
}

// Tab implements frame.Tab and the optional frame interfaces. It reads no host state while
// rendering: rows and extras come from the owner's caches.
type Tab struct {
	rows  func() []Row
	extra func(id string) Extra
	act   Actions

	mu     sync.Mutex
	list   list.List
	fields list.List
	detail string // provider id of the open detail screen, empty on the list
	above  int
	below  int
	total  int
	nrows  int
	signed int
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

// New builds the tab over the owner's cached rows.
func New(rows func() []Row, extra func(id string) Extra, act Actions) *Tab {
	return &Tab{rows: rows, extra: extra, act: act}
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "providers" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Providers" }

// Dirty implements frame.Tab; the tab stages nothing.
func (t *Tab) Dirty() bool { return false }

// Discard implements frame.Tab.
func (t *Tab) Discard() {}

func (t *Tab) extras(id string) Extra {
	if t.extra == nil {
		return Extra{}
	}
	return t.extra(id)
}

func (t *Tab) find(id string) (Row, bool) {
	for _, r := range t.rows() {
		if r.ID == id {
			return r, true
		}
	}
	return Row{}, false
}

// Crumbs implements frame.Crumber.
func (t *Tab) Crumbs() []string {
	t.mu.Lock()
	id := t.detail
	t.mu.Unlock()
	if id == "" {
		return nil
	}
	if r, ok := t.find(id); ok {
		return []string{r.Label}
	}
	return []string{id}
}

// Status implements frame.Statuser.
func (t *Tab) Status(frame.Env) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detail != "" {
		return "read-only"
	}
	return fmt.Sprintf("%d providers · %d signed in", t.nrows, t.signed)
}

// Filterable implements frame.Filterer; only the provider list takes typed text.
func (t *Tab) Filterable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.detail == ""
}

// SetFilter implements frame.Filterer.
func (t *Tab) SetFilter(q string) { t.list.SetFilter(q) }

// Overflow implements frame.Overflower.
func (t *Tab) Overflow() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.above, t.below
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	now := time.Now()
	th := uitheme.Theme{T: env.Theme}
	rows := t.rows()
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		if r, ok := t.find(detail); ok {
			return t.detailBody(th, r, env, now)
		}
		t.mu.Lock()
		t.detail = ""
		t.mu.Unlock()
	}
	signed := 0
	entries := make([]list.Entry, 0, len(rows))
	for _, r := range rows {
		if r.State == SignedIn {
			signed++
		}
		entries = append(entries, list.Entry{ID: r.ID, Search: r.Label + " " + r.ID + " " + strings.Join(r.Aliases, " ")})
	}
	win := t.list.Window(entries, env.Rows)
	t.mu.Lock()
	t.above, t.below, t.nrows, t.signed = win.Above, win.Below, len(rows), signed
	t.mu.Unlock()
	switch {
	case len(rows) == 0:
		return []string{"  reading credential state…"}
	case win.Total == 0:
		return []string{"  no provider matches “" + oneLine(t.list.Query()) + "”"}
	}
	byID := make(map[string]Row, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	lw := labelWidth(rows)
	out := make([]string, 0, len(win.Entries))
	for i, e := range win.Entries {
		out = append(out, rowLine(th, byID[e.ID], t.extras(e.ID), lw, i == win.Selected, env.Width, now))
	}
	return out
}

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s)
}

func (t *Tab) detailBody(th uitheme.Theme, r Row, env frame.Env, now time.Time) []string {
	fields := detailFields(r, t.extras(r.ID), now)
	entries := make([]list.Entry, len(fields))
	lw := 0
	for i, f := range fields {
		entries[i] = list.Entry{ID: f.Label}
		lw = max(lw, tui.VisibleWidth(f.Label))
	}
	win := t.fields.Window(entries, env.Rows)
	t.mu.Lock()
	t.above, t.below = win.Above, win.Below
	t.mu.Unlock()
	out := make([]string, 0, len(win.Entries))
	for i, e := range win.Entries {
		for _, f := range fields {
			if f.Label == e.ID {
				out = append(out, fieldLine(th, f, lw, i == win.Selected, env.Width))
			}
		}
	}
	return out
}

// Description implements frame.Describer.
func (t *Tab) Description(frame.Env) string {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		return "Read-only. Enter on a row marked → opens the tab that owns it."
	}
	r, ok := t.find(t.list.Selected())
	switch {
	case !ok:
		return "Credential state and headline quota per provider."
	case r.State == SignedOut && r.Login != "":
		return "Not signed in. Sign in with " + r.Login + "; pig-auth-plus never logs in itself."
	case r.Note != "":
		return r.Note + ". Enter shows everything known about this provider."
	}
	return "Enter shows everything known about this provider. Credentials are never shown."
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	l := &t.list
	if detail != "" {
		l = &t.fields
	}
	switch {
	case tui.MatchesKey(data, "alt+r"):
		if t.act.Reread != nil {
			t.act.Reread()
		}
	case kb.Matches(data, "tui.select.down"):
		l.Move(1)
	case kb.Matches(data, "tui.select.up"):
		l.Move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		l.Page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		l.Page(-1)
	case kb.Matches(data, "tui.select.confirm"):
		t.open()
	case kb.Matches(data, "tui.select.cancel") && detail != "":
		t.mu.Lock()
		t.detail = ""
		t.mu.Unlock()
	default:
		return false
	}
	return true
}

// open enters the detail of the selected provider, or follows the selected detail row's jump.
func (t *Tab) open() {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail == "" {
		if id := t.list.Selected(); id != "" {
			t.mu.Lock()
			t.detail = id
			t.mu.Unlock()
			t.fields.SetFilter("")
		}
		return
	}
	r, ok := t.find(detail)
	if !ok {
		return
	}
	sel := t.fields.Selected()
	for _, f := range detailFields(r, t.extras(r.ID), time.Now()) {
		if f.Label == sel && f.Jump != "" && t.act.Goto != nil {
			t.act.Goto(f.Jump)
		}
	}
}

// Click implements frame.Pointer: a click selects, a second click opens.
func (t *Tab) Click(row, count int) bool {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	l := &t.list
	if detail != "" {
		l = &t.fields
	}
	id, again := l.Click(row)
	if id == "" {
		return false
	}
	if again || count >= 2 {
		t.open()
	}
	return true
}

// Wheel implements frame.Pointer.
func (t *Tab) Wheel(delta int) {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		t.fields.Scroll(delta)
		return
	}
	t.list.Scroll(delta)
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		return []string{sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "open")}
	}
	return []string{sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "details"), sdk.RawKeyHint("alt+r", "reread")}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Providers
 Credential state for every provider pig-auth-plus knows. Credentials are never shown.
 ● signed in      a usable credential exists; its source and expiry are named
 ◐ token expired  an OAuth token lapsed; PiG refreshes it on the next request
 ○ not signed in  no credential; the sign-in command is shown
 Enter opens a read-only detail with jumps to the tab that owns each value.
 Sign in and out with PiG's /login and /logout. Quota-only providers (codex, xai,
 openrouter, deepseek) use PiG's own login for inference and add usage reporting here.
 alt+r  reread state     type  filter the list     esc  back or close`
