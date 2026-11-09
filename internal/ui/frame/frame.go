// Package frame is the /auth dashboard shell. Every screen has the same fixed lines: top rule
// with breadcrumbs, tab bar, search line, blank line, body, middle rule, description, key hints
// and bottom rule. Tabs render plain body lines already fitted to the width; the frame owns
// everything around them, the typed filter, the help screen, the confirm dialogs, the fixed
// height and the mouse. It never does I/O while rendering.
package frame

import (
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Env is what a tab renders against.
type Env struct {
	// Width is the panel width in columns; every returned line must fit it.
	Width int
	// Rows is the body height the frame leaves a tab. The host side supplies the rows
	// available to the whole frame.
	Rows  int
	Theme sdk.UITheme
	// Level is the session's thinking level; the rules take their color from it.
	Level string
}

// Tab is one dashboard section. Its methods run on the panel's single queue, never
// concurrently, so a tab needs a lock only for state that other goroutines also touch. Tabs
// opt into more of the frame through the optional interfaces in this package.
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
	host func() Env
	tabs []Tab

	mu      sync.Mutex
	active  int
	help    bool
	helpTop int
	modal   *modal
	filters map[string]string
	invalid func()

	// sized is the terminal row count bodyRows was computed for; the height is recomputed only
	// when it changes, so switching tabs or filtering never moves the frame.
	sized    int
	bodyRows int
	layout   layout
}

// layout is the last render's geometry, for mouse hit testing.
type layout struct {
	bodyTop, bodyRows int
	tabs              []span
	modalFirst        int // body row of the first modal option
}

type span struct{ from, to, tab int }

type modal struct {
	crumb   string
	title   string
	options []string
	sel     int
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
// env supplies the current size, theme and thinking level at each render.
func New(env func() Env, start string, tabs ...Tab) *Frame {
	f := &Frame{host: env, tabs: tabs, filters: map[string]string{}}
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

// Choose shows a confirm screen and blocks until the user answers or done is closed. The
// first line of title is the question and the following lines are facts. It must not be
// called from the panel's own queue (a tab key handler); run it in a goroutine.
func (f *Frame) Choose(title string, options []string, done <-chan struct{}) (string, bool) {
	return f.ChooseAt("Confirm", title, options, done)
}

// ChooseAt is [Frame.Choose] with the breadcrumb the screen shows after the active tab, such
// as "Line position" or "Codex › Reset".
func (f *Frame) ChooseAt(crumb, title string, options []string, done <-chan struct{}) (string, bool) {
	m := &modal{crumb: crumb, title: title, options: options, reply: make(chan reply, 1)}
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

// ModalTitle is the title of the open confirm screen, or empty when none is open.
func (f *Frame) ModalTitle() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modal == nil {
		return ""
	}
	return f.modal.title
}

// ModalOptions lists the options of the open confirm screen.
func (f *Frame) ModalOptions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modal == nil {
		return nil
	}
	return append([]string(nil), f.modal.options...)
}

func (f *Frame) env() Env { return f.host() }

// naturalRows is the shared body height: the tallest tab's, so no tab switch changes it.
func (f *Frame) naturalRows() int {
	rows := 0
	for _, t := range f.tabs {
		want := DefaultBodyRows
		if s, ok := t.(Sizer); ok && !s.FullHeight() && s.BodyRows() > 0 {
			want = s.BodyRows()
		}
		rows = max(rows, want)
	}
	return rows
}

// bodyHeight fixes the body height for the terminal's row count; a full-height tab takes
// every available row.
func (f *Frame) bodyHeight(avail int, tab Tab) int {
	if s, ok := tab.(Sizer); ok && s.FullHeight() {
		return max(avail-chromeRows, minBodyRows)
	}
	if f.sized != avail || f.bodyRows == 0 {
		f.sized = avail
		f.bodyRows = min(f.naturalRows(), max(avail-chromeRows, minBodyRows))
	}
	return f.bodyRows
}

// View implements sdk.ViewComponent.
func (f *Frame) View(width int) kit.View {
	env := f.env()
	if width > 0 {
		env.Width = width
	}
	w := max(env.Width, 20)
	// Tab methods run outside the lock: a tab may call Invalidate while holding its own.
	f.mu.Lock()
	tab, help, helpTop, active := f.tabs[f.active], f.help, f.helpTop, f.active
	var m *modal
	var sel int
	if f.modal != nil {
		cp := *f.modal
		m, sel = &cp, f.modal.sel
	}
	rows := f.bodyHeight(env.Rows, tab)
	query := f.filters[tab.ID()]
	f.mu.Unlock()

	benv := Env{Width: w, Rows: rows, Theme: env.Theme, Level: env.Level}
	chrome := uitheme.NewChrome(env.Theme, env.Level)
	th := env.Theme

	var body, crumbs []string
	var status, desc string
	var above, below, modalFirst int
	var hints []hint
	filterable := false
	switch {
	case m != nil:
		body, modalFirst = modalBody(th, m, sel)
		crumbs = []string{m.crumb}
		hints = confirmHints()
	case help:
		all := tab.Help(benv)
		helpTop = min(max(helpTop, 0), max(len(all)-rows, 0))
		body = all[helpTop:min(helpTop+rows, len(all))]
		above, below = helpTop, max(len(all)-helpTop-rows, 0)
		crumbs = []string{"Help"}
		desc = "Full descriptions and bindings; your edits are kept."
		hints = []hint{{sdk.RawKeyHint("↑↓", "scroll"), 1}, {sdk.RawKeyHint("f1", "back"), 0}, {sdk.KeyHint("tui.select.cancel", "back"), 0}}
	default:
		body = tab.Body(benv)
		if c, ok := tab.(Crumber); ok {
			crumbs = c.Crumbs()
		}
		if s, ok := tab.(Statuser); ok {
			status = s.Status(benv)
		}
		if d, ok := tab.(Describer); ok {
			desc = d.Description(benv)
		}
		if o, ok := tab.(Overflower); ok {
			above, below = o.Overflow()
		}
		if fl, ok := tab.(Filterer); ok {
			filterable = fl.Filterable()
		}
		hints = tabHints(tab.Hints(benv), filterable, len(crumbs) > 0)
	}
	if f.anyDirty() {
		status = strings.TrimPrefix(status+" · ", " · ") + th.Fg("warning", "▲ unsaved")
	}

	lines := make([]string, 0, rows+chromeRows)
	lines = append(lines, topRule(chrome, th, w, tab.Title(), crumbs, status, above))
	strip, spans := tabBar(chrome, th, w, f.tabs, active)
	lines = append(lines, strip, searchLine(th, w, query, filterable), "")
	for _, l := range body {
		if len(lines) == 4+rows {
			break
		}
		lines = append(lines, tui.TruncateToWidth(l, w, "…", false))
	}
	for len(lines) < 4+rows {
		lines = append(lines, "")
	}
	bottom := ""
	if below > 0 {
		bottom = th.Fg("dim", "↓ "+strconv.Itoa(below)+" more")
	}
	lines = append(lines,
		ruleLine(w, chrome.Rule, "", "", ""),
		tui.TruncateToWidth(" "+th.Fg("dim", plain(desc)), w, "…", false),
		hintRow(th, w, hints),
		ruleLine(w, chrome.Rule, "", bottom, ""),
	)

	f.mu.Lock()
	f.layout = layout{bodyTop: 4, bodyRows: rows, tabs: spans, modalFirst: modalFirst}
	if help {
		f.helpTop = helpTop
	}
	f.mu.Unlock()
	return kit.View{Root: kit.NewContainer(kit.NewLines(lines))}
}

// plain removes control characters and terminal sequences from untrusted or composed text.
func plain(s string) string {
	s = tui.StripTerminalSequences(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func topRule(chrome uitheme.Chrome, th sdk.UITheme, w int, title string, crumbs []string, status string, above int) string {
	levels := append([]string{"Auth", title}, crumbs...)
	mid := ""
	if above > 0 {
		mid = th.Fg("dim", "↑ "+strconv.Itoa(above)+" more")
	}
	right := ""
	if status != "" {
		right = th.Fg("dim", status)
	}
	return ruleLine(w, chrome.Rule, crumbText(th, levels, w/2), mid, right)
}

// crumbText joins the levels with ›, dropping the earliest levels first so the last one
// survives, and cutting it only when it alone does not fit.
func crumbText(th sdk.UITheme, levels []string, max int) string {
	render := func(ls []string, cut bool) string {
		parts := make([]string, len(ls))
		for i, l := range ls {
			parts[i] = th.Fg("muted", plain(l))
			if i == len(ls)-1 {
				parts[i] = th.Bold(plain(l))
			}
		}
		out := strings.Join(parts, th.Fg("dim", " › "))
		if cut {
			out = th.Fg("dim", "… › ") + out
		}
		return out
	}
	for start := 0; start < len(levels); start++ {
		if out := render(levels[start:], start > 0); tui.VisibleWidth(out) <= max {
			return out
		}
	}
	return tui.TruncateToWidth(render(levels[len(levels)-1:], false), max, "…", false)
}

// tabBar draws the chips, scrolled so the active one is visible, and records each chip's
// columns for the mouse.
func tabBar(chrome uitheme.Chrome, th sdk.UITheme, w int, tabs []Tab, active int) (string, []span) {
	n := len(tabs)
	chips := make([]string, n)
	for i, t := range tabs {
		chips[i] = uitheme.TabSeparator + chrome.Tab(t.Title(), i == active)
	}
	lo, hi := active, active
	build := func(lo, hi int) string {
		out := " "
		if lo > 0 {
			out += th.Fg("dim", "…")
		}
		out += strings.Join(chips[lo:hi+1], "")
		if hi < n-1 {
			out += th.Fg("dim", "…")
		}
		return out
	}
	for {
		grew := false
		if lo > 0 && tui.VisibleWidth(build(lo-1, hi)) <= w {
			lo--
			grew = true
		}
		if hi < n-1 && tui.VisibleWidth(build(lo, hi+1)) <= w {
			hi++
			grew = true
		}
		if !grew {
			break
		}
	}
	line := build(lo, hi)
	cut := lo > 0 || hi < n-1
	if cut {
		count := " " + strconv.Itoa(active+1) + "/" + strconv.Itoa(n)
		if tui.VisibleWidth(line)+len(count) <= w {
			line += th.Fg("dim", count)
		}
	}
	x := 1
	if lo > 0 {
		x++
	}
	var spans []span
	for i := lo; i <= hi; i++ {
		cw := tui.VisibleWidth(chips[i])
		spans = append(spans, span{from: x, to: x + cw, tab: i})
		x += cw
	}
	return tui.TruncateToWidth(line, w, "…", false), spans
}

func searchLine(th sdk.UITheme, w int, q string, filterable bool) string {
	if !filterable {
		return th.Fg("dim", " ⌕")
	}
	if q == "" {
		return tui.TruncateToWidth(th.Fg("dim", " ⌕ Type to filter…"), w, "…", false)
	}
	return tui.TruncateToWidth(" ⌕ "+plain(q)+th.Fg("accent", "▏"), w, "…", false)
}

func modalBody(th sdk.UITheme, m *modal, sel int) (lines []string, firstOption int) {
	for i, line := range strings.Split(m.title, "\n") {
		if i == 0 {
			lines = append(lines, "  "+th.Bold(plain(line)))
			continue
		}
		lines = append(lines, "  "+th.Fg("muted", plain(line)))
	}
	lines = append(lines, "")
	firstOption = len(lines)
	for i, o := range m.options {
		cursor, label := "  ", plain(o)
		if i == sel {
			cursor, label = th.Fg("accent", "→ "), th.Bold(label)
		}
		lines = append(lines, " "+cursor+label)
	}
	return lines, firstOption
}

func (f *Frame) anyDirty() bool {
	for _, t := range f.tabs {
		if t.Dirty() {
			return true
		}
	}
	return false
}

// hint is one key hint; the lowest prio number survives longest.
type hint struct {
	text string
	prio int
}

func confirmHints() []hint {
	return []hint{
		{sdk.RawKeyHint("↑↓", "move"), 1},
		{sdk.KeyHint("tui.select.confirm", "choose"), 1},
		{sdk.KeyHint("tui.select.cancel", "cancel"), 0},
	}
}

// tabHints orders the hints for display (the tab's own first, the frame's last) with
// priorities that keep esc, then help, then the tab's most important hints when space runs out.
func tabHints(tabs []string, filterable, drilled bool) []hint {
	var out []hint
	for i, h := range tabs {
		out = append(out, hint{h, 2 + i})
	}
	out = append(out, hint{sdk.RawKeyHint("tab", "next tab"), 20})
	if filterable {
		out = append(out, hint{"type to filter", 21})
	}
	leave := "close"
	if drilled {
		leave = "back"
	}
	return append(out, hint{sdk.RawKeyHint("f1", "help"), 1}, hint{sdk.KeyHint("tui.select.cancel", leave), 0})
}

// hintRow joins the hints in display order, dropping the lowest-priority ones until it fits.
func hintRow(th sdk.UITheme, w int, hints []hint) string {
	keep := make([]bool, len(hints))
	for i := range keep {
		keep[i] = true
	}
	render := func() string {
		var parts []string
		for i, h := range hints {
			if keep[i] {
				parts = append(parts, h.text)
			}
		}
		return " " + strings.Join(parts, th.Fg("dim", " · "))
	}
	for tui.VisibleWidth(render()) > w {
		drop, worst := -1, 0
		for i, h := range hints {
			if keep[i] && h.prio > worst {
				drop, worst = i, h.prio
			}
		}
		if drop < 0 {
			break
		}
		keep[drop] = false
	}
	return tui.TruncateToWidth(render(), w, "…", false)
}

// HandleInput implements sdk.ViewComponent for keys the view does not bind; the view has no
// focusable node, so every key arrives here.
func (f *Frame) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	f.mu.Lock()
	m, tab, help := f.modal, f.tabs[f.active], f.help
	f.mu.Unlock()
	kb := tui.GetKeybindings()
	switch {
	case tui.MatchesKey(data, "ctrl+c"):
		return f.close(), nil
	case m != nil:
		return f.modalKey(m, data), nil
	case tui.MatchesKey(data, "f1"):
		f.toggleHelp()
	case help:
		f.helpKey(data)
	case tui.MatchesKey(data, "tab"):
		f.switchTab(1)
	case tui.MatchesKey(data, "shift+tab"):
		f.switchTab(-1)
	case tab.Key(data):
	case tui.MatchesKey(data, "alt+up"):
		f.scroll(tab, -1)
	case tui.MatchesKey(data, "alt+down"):
		f.scroll(tab, 1)
	case kb.Matches(data, "tui.select.cancel"):
		if f.clearFilter(tab) {
			break
		}
		return f.close(), nil
	case tui.MatchesKey(data, "left"):
		f.switchTab(-1)
	case tui.MatchesKey(data, "right"):
		f.switchTab(1)
	default:
		f.filterKey(tab, data)
	}
	return sdk.RemoteComponentResult{}, nil
}

func (f *Frame) modalKey(m *modal, data string) sdk.RemoteComponentResult {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.up"):
		f.mu.Lock()
		m.sel = (m.sel + len(m.options) - 1) % len(m.options)
		f.mu.Unlock()
	case kb.Matches(data, "tui.select.down"), tui.MatchesKey(data, "tab"):
		f.mu.Lock()
		m.sel = (m.sel + 1) % len(m.options)
		f.mu.Unlock()
	case kb.Matches(data, "tui.select.confirm"):
		return f.answer(m, reply{choice: m.options[m.sel], ok: true})
	case kb.Matches(data, "tui.select.cancel"):
		return f.answer(m, reply{})
	}
	return sdk.RemoteComponentResult{}
}

// answer resolves the modal: a blocking Choose gets the reply; a frame-owned prompt runs its
// continuation.
func (f *Frame) answer(m *modal, r reply) sdk.RemoteComponentResult {
	if m.then == nil {
		select {
		case m.reply <- r:
		default:
		}
		return sdk.RemoteComponentResult{}
	}
	f.mu.Lock()
	f.modal = nil
	f.mu.Unlock()
	return m.then(r.choice, r.ok)
}

func (f *Frame) helpKey(data string) {
	kb := tui.GetKeybindings()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case kb.Matches(data, "tui.select.cancel"):
		f.help = false
	case kb.Matches(data, "tui.select.up"):
		f.helpTop--
	case kb.Matches(data, "tui.select.down"):
		f.helpTop++
	case kb.Matches(data, "tui.select.pageUp"):
		f.helpTop -= max(f.layout.bodyRows-1, 1)
	case kb.Matches(data, "tui.select.pageDown"):
		f.helpTop += max(f.layout.bodyRows-1, 1)
	}
	f.helpTop = max(f.helpTop, 0)
}

// filterKey feeds typed text and backspace to the search line of a filterable screen.
func (f *Frame) filterKey(tab Tab, data string) {
	fl, ok := tab.(Filterer)
	if !ok || !fl.Filterable() {
		return
	}
	f.mu.Lock()
	q := f.filters[tab.ID()]
	f.mu.Unlock()
	switch {
	case data == "/" && q == "":
		// Slash focuses the search line, which always has focus on a filterable screen.
		return
	case tui.MatchesKey(data, "backspace"):
		_, size := utf8.DecodeLastRuneInString(q)
		q = q[:len(q)-size]
	case tui.MatchesKey(data, "ctrl+u"):
		q = ""
	case typed(data):
		q += data
	default:
		return
	}
	f.setFilter(tab, fl, q)
}

func (f *Frame) setFilter(tab Tab, fl Filterer, q string) {
	f.mu.Lock()
	f.filters[tab.ID()] = q
	f.mu.Unlock()
	fl.SetFilter(q)
}

// clearFilter empties a non-empty filter and reports whether it did.
func (f *Frame) clearFilter(tab Tab) bool {
	fl, ok := tab.(Filterer)
	f.mu.Lock()
	q := f.filters[tab.ID()]
	f.mu.Unlock()
	if !ok || q == "" || !fl.Filterable() {
		return false
	}
	f.setFilter(tab, fl, "")
	return true
}

// typed reports whether data is printable text, not a key sequence.
func typed(data string) bool {
	if data == "" || !utf8.ValidString(data) {
		return false
	}
	for _, r := range data {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (f *Frame) toggleHelp() {
	f.mu.Lock()
	f.help = !f.help
	f.helpTop = 0
	f.mu.Unlock()
}

// Activate shows the tab with the given id; an unknown id changes nothing.
func (f *Frame) Activate(id string) {
	f.mu.Lock()
	for i, t := range f.tabs {
		if t.ID() == id {
			f.active = i
		}
	}
	f.mu.Unlock()
	f.Invalidate()
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
		crumb:   "Discard",
		title:   "Discard unsaved changes in every tab?\nStaged edits are lost; saved values are unchanged.",
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

// HandleMouse implements sdk.MouseHandler. Pi routes the mouse only in fullscreen mode, so
// every action also has a key.
func (f *Frame) HandleMouse(e sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	f.mu.Lock()
	tab, m, help, lay := f.tabs[f.active], f.modal, f.help, f.layout
	f.mu.Unlock()
	row := e.Y - lay.bodyTop
	inBody := row >= 0 && row < lay.bodyRows
	switch {
	case e.Type == sdk.MouseWheel:
		f.wheel(tab, m, help, e.WheelDelta)
	case e.Type != sdk.MouseClick || e.Button != sdk.MouseButtonLeft:
	case m != nil:
		if i := row - lay.modalFirst; inBody && i >= 0 && i < len(m.options) {
			f.mu.Lock()
			m.sel = i
			f.mu.Unlock()
			if e.ClickCount >= 2 {
				return f.answer(m, reply{choice: m.options[i], ok: true}), nil
			}
		}
	case e.Y == 1:
		for _, s := range lay.tabs {
			if e.X >= s.from && e.X < s.to {
				f.mu.Lock()
				f.active = s.tab
				f.mu.Unlock()
			}
		}
	case inBody && !help:
		if p, ok := tab.(Pointer); ok {
			p.Click(row, max(e.ClickCount, 1))
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

// scroll moves the body by delta lines, as the wheel does.
func (f *Frame) scroll(tab Tab, delta int) {
	if p, ok := tab.(Pointer); ok {
		p.Wheel(delta)
	}
}

func (f *Frame) wheel(tab Tab, m *modal, help bool, delta int) {
	switch {
	case m != nil:
	case help:
		f.mu.Lock()
		f.helpTop = max(f.helpTop+delta, 0)
		f.mu.Unlock()
	default:
		if p, ok := tab.(Pointer); ok {
			p.Wheel(delta)
		}
	}
}

var _ sdk.MouseHandler = (*Frame)(nil)
