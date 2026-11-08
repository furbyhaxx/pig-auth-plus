// Package quotas is the Quotas dashboard tab: provider detail, refresh, solo and placement.
package quotas

import (
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

// Tab implements frame.Tab. State read by other goroutines is behind mu.
type Tab struct {
	views   func() []quota.View
	display func() Display
	now     func() time.Time

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
func New(views func() []quota.View, display func() Display, solo string) *Tab {
	return &Tab{views: views, display: display, now: time.Now, solo: solo, sel: solo}
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
	return all[t.top:end]
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
	switch {
	case kb.Matches(data, "tui.select.down") || data == "j":
		t.move(1)
	case kb.Matches(data, "tui.select.up") || data == "k":
		t.move(-1)
	default:
		return false
	}
	return true
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
func (t *Tab) Hints(frame.Env) []string { return nil }

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string {
	return strings.Split(helpText, "\n")
}

const helpText = ` Quotas
 Unknown numbers show "?"; an explicit 0% is real usage.
 Up/Down or k/j select a provider.`
