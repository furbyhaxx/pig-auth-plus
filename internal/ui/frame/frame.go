// Package frame is the /auth dashboard shell: a tab strip, one active tab body, a hint row,
// an in-panel choice dialog and discard protection. Tabs render plain lines already fitted to
// the width; the frame never does I/O while rendering.
package frame

import (
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Env is what a tab renders against.
type Env struct {
	// Width is the panel width in columns; every returned line must fit it.
	Width int
	// Rows is the body height the frame leaves the tab.
	Rows  int
	Theme sdk.UITheme
}

// Tab is one dashboard section. Its methods run on the panel's single queue, never
// concurrently, so a tab needs a lock only for state that other goroutines also touch.
type Tab interface {
	ID() string
	Title() string
	// Body returns the lines of the tab at env.Width, at most env.Rows of them.
	Body(env Env) []string
	// Hints are the tab's key hints in priority order; the frame drops from the end.
	Hints(env Env) []string
	// Help is the full help text for the tab.
	Help(env Env) []string
	// Key handles a key the frame did not take and reports whether it was used.
	Key(data string) bool
	// Dirty reports staged edits that closing would discard.
	Dirty() bool
	// Discard drops staged edits.
	Discard()
}

// Frame is the focused dashboard component; pass it to Context.Custom.
type Frame struct {
	host    func() Env
	tabs    []Tab
	mu      sync.Mutex
	active  int
	help    bool
	modal   *modal
	invalid func()
}

type modal struct {
	title   string
	options []string
	// reply answers a blocking Choose; then answers a choice the frame itself asked for and
	// may close the panel.
	reply chan reply
	then  func(choice string, ok bool) sdk.RemoteComponentResult
}

type reply struct {
	choice string
	ok     bool
}

// New builds a frame over tabs, starting on the tab with id start (the first if unknown).
// env supplies the current size and theme at each render.
func New(env func() Env, start string, tabs ...Tab) *Frame {
	f := &Frame{host: env, tabs: tabs}
	for i, t := range tabs {
		if t.ID() == start {
			f.active = i
		}
	}
	return f
}

// SetInvalidate implements sdk.RemoteComponentInvalidator; tabs call Invalidate when their
// data changes without input.
func (f *Frame) SetInvalidate(fn func()) {
	f.mu.Lock()
	f.invalid = fn
	f.mu.Unlock()
}

// Invalidate asks the host to render again.
func (f *Frame) Invalidate() {
	f.mu.Lock()
	fn := f.invalid
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Choose shows a modal choice and blocks until the user answers or done is closed. It must
// not be called from the panel's own queue (a tab key handler); run it in a goroutine.
func (f *Frame) Choose(title string, options []string, done <-chan struct{}) (string, bool) {
	m := &modal{title: title, options: options, reply: make(chan reply, 1)}
	f.mu.Lock()
	f.modal = m
	f.mu.Unlock()
	f.Invalidate()
	defer func() {
		f.mu.Lock()
		if f.modal == m {
			f.modal = nil
		}
		f.mu.Unlock()
		f.Invalidate()
	}()
	select {
	case r := <-m.reply:
		return r.choice, r.ok
	case <-done:
		return "", false
	}
}

func (f *Frame) env() Env { return f.host() }

const chromeRows = 5 // two borders, title, tab strip, hint row

// View implements sdk.ViewComponent.
func (f *Frame) View(int) kit.View {
	env := f.env()
	w := max(env.Width, 20)
	f.mu.Lock()
	tab, help, m := f.tabs[f.active], f.help, f.modal
	f.mu.Unlock()
	rows := max(env.Rows-chromeRows, 3)
	benv := Env{Width: w, Rows: rows, Theme: env.Theme}

	var body []string
	switch {
	case m != nil:
		return f.modalView(env, tab, m)
	case help:
		body = fit(tab.Help(benv), w, rows)
	default:
		body = fit(tab.Body(benv), w, rows)
	}
	root := kit.NewContainer(
		kit.NewDynamicBorder("borderAccent"),
		kit.NewLines(f.header(env, tab)),
		kit.NewLines(body),
		kit.NewLines([]string{f.hintRow(env, tab, w)}),
		kit.NewDynamicBorder("borderAccent"),
	)
	return kit.View{Root: root}
}

func (f *Frame) modalView(env Env, tab Tab, m *modal) kit.View {
	items := make([]kit.SelectItem, len(m.options))
	for i, o := range m.options {
		items[i] = kit.SelectItem{Value: o, Label: o}
	}
	return kit.View{
		Root: kit.NewContainer(
			kit.NewDynamicBorder("borderAccent"),
			kit.NewLines(fit([]string{env.Theme.Bold(m.title)}, max(env.Width, 20), 3)),
			kit.NewSelectList("modal", items, min(len(items), 8)),
			kit.NewDynamicBorder("borderAccent"),
		),
		Focus: "modal",
	}
}

// header is the title row and the tab strip; the active tab survives every width.
func (f *Frame) header(env Env, active Tab) []string {
	w := max(env.Width, 20)
	t := env.Theme
	title := t.Bold(t.Fg("accent", "Auth")) + t.Fg("dim", " › ") + active.Title()
	if f.anyDirty() {
		title += t.Fg("warning", " · ▲ unsaved")
	}
	var parts []string
	for _, tab := range f.tabs {
		label := tab.Title()
		if tab == active {
			label = t.Bold(t.Fg("accent", "["+label+"]"))
		} else {
			label = t.Fg("muted", " "+label+" ")
		}
		parts = append(parts, label)
	}
	return []string{
		tui.TruncateToWidth(" "+title, w, "…", false),
		tui.TruncateToWidth(" "+strings.Join(parts, ""), w, "…", false),
	}
}

func (f *Frame) anyDirty() bool {
	for _, t := range f.tabs {
		if t.Dirty() {
			return true
		}
	}
	return false
}

// hintRow joins the tab's hints with the frame's, dropping from the end until it fits.
func (f *Frame) hintRow(env Env, tab Tab, w int) string {
	hints := append(tab.Hints(env), sdk.RawKeyHint("tab", "next"), sdk.RawKeyHint("f1", "help"), sdk.KeyHint("tui.select.cancel", "close"))
	if f.help {
		hints = []string{sdk.RawKeyHint("f1", "back"), sdk.KeyHint("tui.select.cancel", "back")}
	}
	for n := len(hints); n > 0; n-- {
		row := " " + strings.Join(hints[:n], env.Theme.Fg("dim", " · "))
		if tui.VisibleWidth(row) <= w {
			return row
		}
	}
	return tui.TruncateToWidth(" "+hints[0], w, "…", false)
}

// fit cuts lines to the width and keeps at most rows of them.
func fit(lines []string, width, rows int) []string {
	out := make([]string, 0, min(len(lines), rows))
	for _, l := range lines {
		if len(out) == rows {
			break
		}
		out = append(out, tui.TruncateToWidth(l, width, "…", false))
	}
	return out
}

// HandleViewEvent implements sdk.ViewEventHandler for the modal list.
func (f *Frame) HandleViewEvent(e kit.Event) (sdk.RemoteComponentResult, error) {
	f.mu.Lock()
	m := f.modal
	f.mu.Unlock()
	if m == nil || e.Node != "modal" {
		return sdk.RemoteComponentResult{}, nil
	}
	var r reply
	switch e.Type {
	case kit.EventSelect:
		if e.Item == nil {
			return sdk.RemoteComponentResult{}, nil
		}
		r = reply{choice: e.Item.Value, ok: true}
	case kit.EventCancel:
	default:
		return sdk.RemoteComponentResult{}, nil
	}
	if m.then == nil {
		m.reply <- r
		return sdk.RemoteComponentResult{}, nil
	}
	f.mu.Lock()
	f.modal = nil
	f.mu.Unlock()
	return m.then(r.choice, r.ok), nil
}

// HandleInput implements sdk.ViewComponent for keys the focused list does not bind.
func (f *Frame) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	f.mu.Lock()
	m, tab := f.modal, f.tabs[f.active]
	f.mu.Unlock()
	if m != nil {
		return sdk.RemoteComponentResult{}, nil
	}
	kb := tui.GetKeybindings()
	switch {
	case tui.MatchesKey(data, "ctrl+c"):
		return f.close(), nil
	case tui.MatchesKey(data, "f1"):
		f.toggleHelp()
	case f.help:
		if kb.Matches(data, "tui.select.cancel") {
			f.toggleHelp()
		}
	case tui.MatchesKey(data, "tab"):
		f.switchTab(1)
	case tui.MatchesKey(data, "shift+tab"):
		f.switchTab(-1)
	case tab.Key(data):
	case kb.Matches(data, "tui.select.cancel"):
		return f.close(), nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (f *Frame) toggleHelp() {
	f.mu.Lock()
	f.help = !f.help
	f.mu.Unlock()
}

func (f *Frame) switchTab(dir int) {
	f.mu.Lock()
	f.active = (f.active + dir + len(f.tabs)) % len(f.tabs)
	f.mu.Unlock()
}

// close ends the panel at once when nothing is staged; otherwise it asks first.
func (f *Frame) close() sdk.RemoteComponentResult {
	if !f.anyDirty() {
		return sdk.RemoteComponentResult{Done: true}
	}
	const keep, drop = "Keep editing", "Discard changes"
	f.mu.Lock()
	f.modal = &modal{
		title:   "Discard unsaved changes in every tab?",
		options: []string{keep, drop},
		then: func(choice string, ok bool) sdk.RemoteComponentResult {
			if !ok || choice != drop {
				return sdk.RemoteComponentResult{}
			}
			for _, t := range f.tabs {
				t.Discard()
			}
			return sdk.RemoteComponentResult{Done: true}
		},
	}
	f.mu.Unlock()
	return sdk.RemoteComponentResult{}
}
