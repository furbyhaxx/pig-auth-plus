// Package quotas is the Quotas dashboard tab: provider detail, refresh, solo and placement.
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

// Tab implements frame.Tab. State read by other goroutines is behind mu.
type Tab struct {
	views   func() []quota.View
	display func() Display
	actions Actions
	now     func() time.Time

	notice string
	warn   bool

	mu   sync.Mutex
	solo string
	sel  string
	top  int
	rows int
	last []span
}

type span struct {
	id         string
	start, end int
}

var _ frame.Tab = (*Tab)(nil)

// New builds the tab. views is the engine's cached view; solo starts the tab on one provider
// (empty shows all).
func New(views func() []quota.View, display func() Display, actions Actions, solo string) *Tab {
	return &Tab{views: views, display: display, actions: actions, now: time.Now, solo: solo, sel: solo}
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

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	d := t.display()
	t.mu.Lock()
	defer t.mu.Unlock()
	views := visible(t.views(), t.solo, d.HideSignedOut)
	if len(views) == 0 {
		return []string{"  " + emptyText(t.solo)}
	}
	notice := t.noticeLines(env)
	env.Rows = max(env.Rows-len(notice), 1)
	if !has(views, t.sel) {
		t.sel = views[0].Source.ID
	}
	blocks := render(env, views, d, t.sel, t.now())
	var all []string
	t.last = t.last[:0]
	for i, b := range blocks {
		if i > 0 {
			all = append(all, "")
		}
		t.last = append(t.last, span{id: b.id, start: len(all), end: len(all) + len(b.lines)})
		all = append(all, b.lines...)
	}
	t.rows = env.Rows
	t.scrollToSelected(env.Rows, len(all))
	end := min(t.top+env.Rows, len(all))
	return append(all[t.top:end:end], notice...)
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

func has(views []quota.View, id string) bool {
	for _, v := range views {
		if v.Source.ID == id {
			return true
		}
	}
	return false
}

// scrollToSelected keeps the selected provider's heading in view.
func (t *Tab) scrollToSelected(rows, total int) {
	for _, s := range t.last {
		if s.id != t.sel {
			continue
		}
		// A block partly in view may be scrolled within; only a block wholly out of view snaps.
		if s.end <= t.top || s.start >= t.top+rows {
			t.top = s.start
		}
	}
	t.top = max(0, min(t.top, total-rows))
}

// Key implements frame.Tab: selection, and scrolling inside a provider taller than the view.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	t.mu.Lock()
	t.notice = ""
	t.mu.Unlock()
	switch {
	case data == "r":
		t.act(func(id string) { t.actions.Refresh(id) })
	case data == "R":
		if t.actions.Refresh != nil {
			t.actions.Refresh("")
		}
	case data == "a":
		t.toggleSolo()
	case data == "p":
		if t.actions.Placement != nil {
			t.actions.Placement()
		}
	case data == "x":
		t.act(func(id string) {
			if t.actions.Reset != nil && t.canReset(id) {
				t.actions.Reset(id)
			} else {
				t.SetNotice("No reset credits for this provider.", true)
			}
		})
	case kb.Matches(data, "tui.select.down") || data == "j":
		t.move(1)
	case kb.Matches(data, "tui.select.up") || data == "k":
		t.move(-1)
	default:
		return false
	}
	return true
}

// act runs fn with the selected provider id, if any.
func (t *Tab) act(fn func(id string)) {
	t.mu.Lock()
	id := t.sel
	t.mu.Unlock()
	if id != "" && fn != nil {
		fn(id)
	}
}

func (t *Tab) toggleSolo() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.solo != "" {
		t.solo = ""
	} else {
		t.solo = t.sel
	}
	t.top = 0
}

func (t *Tab) canReset(id string) bool {
	for _, v := range t.views() {
		if v.Source.ID == id {
			return v.Source.Resets != nil
		}
	}
	return false
}

func (t *Tab) move(dir int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := -1
	for i, s := range t.last {
		if s.id == t.sel {
			idx = i
		}
	}
	if idx < 0 {
		return
	}
	cur := t.last[idx]
	rows := t.rows
	if dir > 0 && cur.end > t.top+rows {
		t.top++
		return
	}
	if dir < 0 && t.top > cur.start {
		t.top--
		return
	}
	if next := idx + dir; next >= 0 && next < len(t.last) {
		t.sel = t.last[next].id
	}
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	t.mu.Lock()
	sel, solo := t.sel, t.solo
	t.mu.Unlock()
	soloLabel := "solo"
	if solo != "" {
		soloLabel = "all"
	}
	hints := []string{sdk.RawKeyHint("r", "refresh"), sdk.RawKeyHint("R", "refresh all"), sdk.RawKeyHint("a", soloLabel), sdk.RawKeyHint("p", "line placement")}
	if t.canReset(sel) {
		hints = append(hints, sdk.RawKeyHint("x", "reset credit"))
	}
	return hints
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string {
	return strings.Split(helpText, "\n")
}

const helpText = ` Quotas
 Unknown numbers show "?"; an explicit 0% is real usage; a missing limit is not "unlimited".
 Up/Down or k/j  select a provider, scroll one that is taller than the view
 r  refresh the selected provider      R  refresh every enabled provider
 a  toggle showing only the selected provider
 p  choose where the quota line appears and where to save it
 x  spend a reset credit (asks first; only on providers that offer them)
 Refresh respects a provider's rate-limit hold; the panel never fetches by itself.`
