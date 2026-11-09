// Package quotas is the Quotas dashboard tab: one summary row per provider and a detail screen
// per provider with every window, burn rate and projected exhaustion.
package quotas

import (
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/list"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Actions are the effects the tab starts; each runs asynchronously in the owner, so a key
// handler never blocks on I/O or a dialog.
type Actions struct {
	// Refresh forces a refresh of one provider id, or of every enabled provider when id is empty.
	Refresh func(id string)
	// Placement opens the line-placement chooser.
	Placement func()
	// Reset starts the reset-credit flow for a provider id.
	Reset func(id string)
}

// Tab implements frame.Tab and the optional frame interfaces. State read by other goroutines
// is behind mu.
type Tab struct {
	views   func() []quota.View
	display func() Display
	actions Actions
	now     func() time.Time

	mu     sync.Mutex
	list   list.List
	detail string // provider id of the open detail screen, empty on the list
	top    int    // first visible line of the detail screen
	lines  int    // detail line count at the last render
	rows   int
	above  int
	below  int
	notice string
	warn   bool
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

// New builds the tab. views is the engine's cached view; open starts on that provider's detail
// screen (empty starts on the list).
func New(views func() []quota.View, display func() Display, actions Actions, open string) *Tab {
	return &Tab{views: views, display: display, actions: actions, now: time.Now, detail: open}
}

// SetNotice shows feedback from an action until the next key.
func (t *Tab) SetNotice(text string, warn bool) {
	t.mu.Lock()
	t.notice, t.warn = text, warn
	t.mu.Unlock()
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "quotas" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Quotas" }

// Dirty implements frame.Tab; the tab stages nothing.
func (t *Tab) Dirty() bool { return false }

// Discard implements frame.Tab.
func (t *Tab) Discard() {}

// Filterable implements frame.Filterer: the provider list filters, an open detail does not.
func (t *Tab) Filterable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.detail == ""
}

// SetFilter implements frame.Filterer.
func (t *Tab) SetFilter(q string) { t.list.SetFilter(q) }

func (t *Tab) find(id string) (quota.View, bool) {
	for _, v := range t.views() {
		if v.Source.ID == id {
			return v, true
		}
	}
	return quota.View{}, false
}

// Crumbs implements frame.Crumber.
func (t *Tab) Crumbs() []string {
	t.mu.Lock()
	id := t.detail
	t.mu.Unlock()
	if id == "" {
		return nil
	}
	if v, ok := t.find(id); ok {
		return []string{v.Source.Label}
	}
	return []string{id}
}

// Status implements frame.Statuser.
func (t *Tab) Status(frame.Env) string {
	now := t.now()
	t.mu.Lock()
	id := t.detail
	t.mu.Unlock()
	if id != "" {
		v, ok := t.find(id)
		if !ok || v.FetchedAt == 0 {
			return "not fetched yet"
		}
		state := "ok"
		switch {
		case v.Snapshot != nil && v.Snapshot.NeedsAuth:
			state = "login required"
		case v.Backoff != nil && v.Backoff.Until > now.UnixMilli():
			state = "held"
		case v.Snapshot != nil && v.Snapshot.Error != "":
			state = "error"
		}
		return "fetched " + short(now.Sub(time.UnixMilli(v.FetchedAt))) + " ago · " + state
	}
	var newest int64
	for _, v := range t.views() {
		newest = max(newest, v.FetchedAt)
	}
	parts := []string{}
	if newest > 0 {
		parts = append(parts, "refreshed "+short(now.Sub(time.UnixMilli(newest)))+" ago")
	}
	if p := placementLabel(t.display().Placement); p != "" {
		parts = append(parts, "line "+p)
	}
	return strings.Join(parts, " · ")
}

func placementLabel(v string) string {
	switch v {
	case "above-editor":
		return "above editor"
	case "below-editor-first":
		return "below editor"
	case "below-editor-last":
		return "in footer"
	case "off":
		return "hidden"
	}
	return ""
}

// Overflow implements frame.Overflower.
func (t *Tab) Overflow() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.above, t.below
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	d := t.display()
	now := t.now()
	th := uitheme.Theme{T: env.Theme}
	t.mu.Lock()
	detail := t.detail
	notice := t.noticeLines(env)
	t.mu.Unlock()
	env.Rows = max(env.Rows-len(notice), 1)
	if detail != "" {
		if v, ok := t.find(detail); ok {
			return append(t.detailBody(th, v, d, env, now), notice...)
		}
		t.mu.Lock()
		t.detail = ""
		t.mu.Unlock()
	}
	views := visible(t.views(), "", d.HideSignedOut)
	if len(views) == 0 {
		return append([]string{"  " + emptyText("")}, notice...)
	}
	entries := make([]list.Entry, len(views))
	byID := make(map[string]int, len(views))
	for i, v := range views {
		entries[i] = list.Entry{ID: v.Source.ID, Search: v.Source.Label}
		byID[v.Source.ID] = i
	}
	win := t.list.Window(entries, env.Rows)
	if len(win.Entries) == 0 {
		return append([]string{"  no provider matches “" + line.Plain(t.list.Query()) + "”"}, notice...)
	}
	t.mu.Lock()
	t.above, t.below, t.rows = win.Above, win.Below, env.Rows
	t.mu.Unlock()
	shown := make([]quota.View, len(win.Entries))
	for i, e := range win.Entries {
		shown[i] = views[byID[e.ID]]
	}
	return append(summaryRows(th, shown, d, win.Selected, env.Width, now), notice...)
}

func (t *Tab) detailBody(th uitheme.Theme, v quota.View, d Display, env frame.Env, now time.Time) []string {
	all := detailLines(th, v, d, env.Width, now)
	if v.Snapshot == nil || v.Snapshot.NeedsAuth {
		text := describe(v, now, true)
		if v.Snapshot == nil {
			text = "Nothing fetched yet. Press alt+r to refresh."
		}
		all = nil
		for _, l := range tui.WrapTextWithAnsi(text, max(env.Width-2, 10)) {
			all = append(all, "  "+paint(th, l, "muted"))
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows, t.lines = env.Rows, len(all)
	t.top = max(0, min(t.top, len(all)-env.Rows))
	end := min(t.top+env.Rows, len(all))
	t.above, t.below = t.top, len(all)-end
	return all[t.top:end:end]
}

func (t *Tab) noticeLines(env frame.Env) []string {
	if t.notice == "" {
		return nil
	}
	th := uitheme.Theme{T: env.Theme}
	token, glyph := "muted", "· "
	if t.warn {
		token, glyph = "warning", "▲ "
	}
	var out []string
	for i, para := range strings.Split(t.notice, "\n") {
		prefix := "  "
		if i == 0 {
			prefix = glyph
		}
		for _, l := range tui.WrapTextWithAnsi(prefix+line.Plain(para), max(env.Width-2, 10)) {
			out = append(out, " "+paint(th, l, token))
		}
	}
	return out
}

func emptyText(solo string) string {
	if solo != "" {
		return "No quota source named " + solo + "."
	}
	return "No quota providers are enabled or signed in."
}

// selected is the provider the actions apply to: the open detail, else the list cursor.
func (t *Tab) selected() string {
	t.mu.Lock()
	id := t.detail
	t.mu.Unlock()
	if id != "" {
		return id
	}
	return t.list.Selected()
}

// Description implements frame.Describer.
func (t *Tab) Description(frame.Env) string {
	v, ok := t.find(t.selected())
	if !ok {
		return "Usage windows and credits per provider; unknown stays unknown."
	}
	t.mu.Lock()
	inDetail := t.detail != ""
	t.mu.Unlock()
	return describe(v, t.now(), inDetail)
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	t.notice = ""
	detail := t.detail
	t.mu.Unlock()
	switch {
	case tui.MatchesKey(data, frame.KeyRefresh):
		t.act(func(id string) { t.actions.Refresh(id) })
	case tui.MatchesKey(data, frame.KeyRefreshAll) && detail == "":
		if t.actions.Refresh != nil {
			t.actions.Refresh("")
		}
	case tui.MatchesKey(data, frame.KeyPosition) && detail == "":
		if t.actions.Placement != nil {
			t.actions.Placement()
		}
	case tui.MatchesKey(data, frame.KeyReset):
		t.act(func(id string) {
			if t.actions.Reset != nil && t.canReset(id) {
				t.actions.Reset(id)
			} else {
				t.SetNotice("No reset credits for this provider.", true)
			}
		})
	case kb.Matches(data, "tui.select.down"):
		t.move(1)
	case kb.Matches(data, "tui.select.up"):
		t.move(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		t.page(1)
	case kb.Matches(data, "tui.select.pageUp"):
		t.page(-1)
	case kb.Matches(data, "tui.select.confirm") && detail == "":
		t.open(t.list.Selected())
	case kb.Matches(data, "tui.select.cancel") && detail != "":
		t.mu.Lock()
		t.detail, t.top = "", 0
		t.mu.Unlock()
	default:
		return false
	}
	return true
}

func (t *Tab) open(id string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	t.detail, t.top = id, 0
	t.mu.Unlock()
}

// act runs fn with the selected provider id, if any.
func (t *Tab) act(fn func(id string)) {
	if id := t.selected(); id != "" && fn != nil {
		fn(id)
	}
}

func (t *Tab) canReset(id string) bool {
	v, ok := t.find(id)
	return ok && v.Source.Resets != nil
}

func (t *Tab) move(dir int) {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		t.scroll(dir)
		return
	}
	t.list.Move(dir)
}

func (t *Tab) page(dir int) {
	t.mu.Lock()
	detail, rows := t.detail, t.rows
	t.mu.Unlock()
	if detail != "" {
		t.scroll(dir * max(rows-1, 1))
		return
	}
	t.list.Page(dir)
}

func (t *Tab) scroll(delta int) {
	t.mu.Lock()
	t.top = max(0, min(t.top+delta, t.lines-t.rows))
	t.mu.Unlock()
}

// Click implements frame.Pointer: a click selects, a second click opens the detail.
func (t *Tab) Click(row, count int) bool {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		return false
	}
	id, again := t.list.Click(row)
	if id == "" {
		return false
	}
	if again || count >= 2 {
		t.open(id)
	}
	return true
}

// Wheel implements frame.Pointer.
func (t *Tab) Wheel(delta int) {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	if detail != "" {
		t.scroll(delta)
		return
	}
	t.list.Scroll(delta)
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	detail := t.detail
	t.mu.Unlock()
	hints := []string{}
	if detail != "" {
		hints = append(hints, sdk.RawKeyHint("↑↓", "scroll"), sdk.RawKeyHint(frame.KeyRefresh, "refresh"))
	} else {
		hints = append(hints, sdk.RawKeyHint("↑↓", "move"), sdk.KeyHint("tui.select.confirm", "details"), sdk.RawKeyHint(frame.KeyRefresh, "refresh"), sdk.RawKeyHint(frame.KeyRefreshAll, "all"), sdk.RawKeyHint(frame.KeyPosition, "position"))
	}
	if t.canReset(t.selected()) {
		hints = append(hints, sdk.RawKeyHint(frame.KeyReset, "reset"))
	}
	return hints
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Quotas
 One row per provider with its important windows; Enter opens every window with burn rate
 and projected exhaustion. Unknown numbers show "?"; an explicit 0% is real usage; a
 missing limit is not "unlimited". The panel never fetches by itself.
 Up/Down  move or scroll       Enter  open the provider      esc  back or close
 alt+r  refresh the selected provider      alt+a  refresh every enabled provider
 alt+p  choose where the quota line appears and where to save it
 alt+x  spend a reset credit (asks first; only on providers that offer them)
 Refresh respects a provider's rate-limit hold.`
