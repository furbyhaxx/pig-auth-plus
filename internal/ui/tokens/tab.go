// Package tokens is the Tokens dashboard tab: the live context count, its sources and the
// full-request breakdown.
package tokens

import (
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab and the optional frame interfaces. The owner pushes breakdown state
// with SetBreakdown.
type Tab struct {
	report    func() *tokens.Report
	display   func() Display
	recount   func()
	mu        sync.Mutex
	breakdown Breakdown
	top       int
	last      int
	rows      int
}

var (
	_ frame.Tab        = (*Tab)(nil)
	_ frame.Statuser   = (*Tab)(nil)
	_ frame.Describer  = (*Tab)(nil)
	_ frame.Filterer   = (*Tab)(nil)
	_ frame.Overflower = (*Tab)(nil)
	_ frame.Pointer    = (*Tab)(nil)
)

// New builds the tab. report is the tracker's latest report; recount starts a fresh count and
// breakdown asynchronously.
func New(report func() *tokens.Report, display func() Display, recount func()) *Tab {
	return &Tab{report: report, display: display, recount: recount}
}

// SetBreakdown replaces the displayed breakdown.
func (t *Tab) SetBreakdown(b Breakdown) {
	t.mu.Lock()
	t.breakdown = b
	t.mu.Unlock()
}

// ID implements frame.Tab.
func (t *Tab) ID() string { return "tokens" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Tokens" }

// Dirty implements frame.Tab; the tab stages nothing.
func (t *Tab) Dirty() bool { return false }

// Discard implements frame.Tab.
func (t *Tab) Discard() {}

// Filterable implements frame.Filterer: the screen has a handful of rows and `r` recounts, so
// typed text never filters.
func (t *Tab) Filterable() bool { return false }

// SetFilter implements frame.Filterer.
func (t *Tab) SetFilter(string) {}

// Status implements frame.Statuser: the model the count is for.
func (t *Tab) Status(frame.Env) string {
	r := t.report()
	if r == nil {
		return "no model"
	}
	return line.Plain(r.Provider + "/" + r.Model)
}

// Description implements frame.Describer.
func (t *Tab) Description(frame.Env) string {
	return "Live session context count, where it came from, and what fills it."
}

// Overflow implements frame.Overflower.
func (t *Tab) Overflow() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.top, max(t.last-t.top-t.rows, 0)
}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	lines := Lines(uitheme.Theme{T: env.Theme}, t.report(), t.snapshot(), t.display(), env.Width)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows, t.last = env.Rows, len(lines)
	t.top = max(0, min(t.top, len(lines)-env.Rows))
	end := min(t.top+env.Rows, len(lines))
	return lines[t.top:end:end]
}

func (t *Tab) snapshot() Breakdown {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.breakdown
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case tui.MatchesKey(data, frame.KeyRefresh):
		if t.recount != nil {
			t.recount()
		}
	case kb.Matches(data, "tui.select.down"):
		t.Wheel(1)
	case kb.Matches(data, "tui.select.up"):
		t.Wheel(-1)
	case kb.Matches(data, "tui.select.pageDown"):
		t.Wheel(max(t.pageRows()-1, 1))
	case kb.Matches(data, "tui.select.pageUp"):
		t.Wheel(-max(t.pageRows()-1, 1))
	default:
		return false
	}
	return true
}

func (t *Tab) pageRows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rows
}

// Click implements frame.Pointer; nothing on this screen is selectable.
func (t *Tab) Click(int, int) bool { return false }

// Wheel implements frame.Pointer.
func (t *Tab) Wheel(delta int) {
	t.mu.Lock()
	t.top = max(0, min(t.top+delta, t.last-t.rows))
	t.mu.Unlock()
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string {
	return []string{sdk.RawKeyHint("↑↓", "scroll"), sdk.RawKeyHint(frame.KeyRefresh, "recount")}
}

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Tokens
 Context is what the next request carries: the last reply's usage plus the messages after it.
 ● exact (usage)  reported by the provider for a real request
 ◐ count API      the provider counted the full request; not billed usage
 ≈ estimate       measured characters per token; shown whenever a total includes one
 Pi is the host's own estimate; Δ is the difference. Breakdown recounts the full request and
 shows each part's share of that request, not of the context window.
alt+r  recount now     ↑↓  scroll     pgup/pgdn  page`
