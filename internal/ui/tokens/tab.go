// Package tokens is the Tokens dashboard tab: the live context count, its sources and the
// full-request breakdown.
package tokens

import (
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab. The owner pushes breakdown state with SetBreakdown.
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

var _ frame.Tab = (*Tab)(nil)

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
	case data == "r":
		if t.recount != nil {
			t.recount()
		}
	case kb.Matches(data, "tui.select.down"):
		t.scroll(1)
	case kb.Matches(data, "tui.select.up"):
		t.scroll(-1)
	default:
		return false
	}
	return true
}

func (t *Tab) scroll(delta int) {
	t.mu.Lock()
	t.top = max(0, min(t.top+delta, t.last-t.rows))
	t.mu.Unlock()
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string { return []string{sdk.RawKeyHint("r", "recount")} }

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Tokens
 Total is the context the next request carries: the last reply's usage plus the messages after it.
 exact (usage)  reported by the provider for a real request
 count API      the provider counted the full request; not billed usage
 estimate       measured characters per token; labelled whenever a total includes one
 Delta compares the total with the host's own estimate. Breakdown recounts the full request.
 r  recount now          Up/Down  scroll`
