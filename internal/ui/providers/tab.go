package providers

import (
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Tab implements frame.Tab over rows the owner supplies; it reads no host state while rendering.
type Tab struct {
	rows    func() []Row
	refresh func()
	mu      sync.Mutex
	top     int
	last    int
	height  int
}

var _ frame.Tab = (*Tab)(nil)

// New builds the tab. refresh asks the owner to reread credential state in the background.
func New(rows func() []Row, refresh func()) *Tab { return &Tab{rows: rows, refresh: refresh} }

// ID implements frame.Tab.
func (t *Tab) ID() string { return "providers" }

// Title implements frame.Tab.
func (t *Tab) Title() string { return "Providers" }

// Dirty implements frame.Tab; the tab stages nothing.
func (t *Tab) Dirty() bool { return false }

// Discard implements frame.Tab.
func (t *Tab) Discard() {}

// Body implements frame.Tab.
func (t *Tab) Body(env frame.Env) []string {
	rows := t.rows()
	lines := Lines(uitheme.Theme{T: env.Theme}, rows, "", env.Width, time.Now())
	if len(rows) == 0 {
		lines = []string{"  reading credential state…"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.height, t.last = env.Rows, len(lines)
	t.top = max(0, min(t.top, len(lines)-env.Rows))
	end := min(t.top+env.Rows, len(lines))
	return lines[t.top:end:end]
}

// Key implements frame.Tab.
func (t *Tab) Key(data string) bool {
	kb := tui.GetKeybindings()
	switch {
	case data == "r":
		if t.refresh != nil {
			t.refresh()
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
	t.top = max(0, min(t.top+delta, t.last-t.height))
	t.mu.Unlock()
}

// Hints implements frame.Tab.
func (t *Tab) Hints(frame.Env) []string { return []string{sdk.RawKeyHint("r", "reread")} }

// Help implements frame.Tab.
func (t *Tab) Help(frame.Env) []string { return strings.Split(helpText, "\n") }

const helpText = ` Providers
 Credential state for every provider pig-auth-plus knows. Credentials are never shown.
 ✓ signed in    a usable credential exists; its source is named
 ○ signed out   no credential; the sign-in command is shown
 Sign in and out with PiG's /login and /logout. Quota-only providers (codex, xai,
 openrouter, deepseek) use PiG's own login for inference and add usage reporting here.
 r  reread state          Up/Down  scroll`
