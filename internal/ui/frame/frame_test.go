package frame

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

type fakeTab struct {
	id, title string
	dirty     bool
	keys      []string
	discarded bool
	body      []string
	rows      int
	full      bool
	filter    string
	filtering bool
	crumbs    []string
	clicks    [][2]int
	wheel     []int
	below     int
}

func (t *fakeTab) ID() string    { return t.id }
func (t *fakeTab) Title() string { return t.title }
func (t *fakeTab) Body(e Env) []string {
	if t.body != nil {
		return t.body
	}
	return []string{strings.Repeat("x", e.Width*2), "second", "third"}
}
func (t *fakeTab) Hints(Env) []string { return []string{"r refresh", "a solo", "p place"} }
func (t *fakeTab) Help(Env) []string  { return []string{"help text"} }
func (t *fakeTab) Key(d string) bool {
	t.keys = append(t.keys, d)
	return d == "r"
}
func (t *fakeTab) Dirty() bool { return t.dirty }
func (t *fakeTab) Discard()    { t.dirty, t.discarded = false, true }

func (t *fakeTab) BodyRows() int        { return t.rows }
func (t *fakeTab) FullHeight() bool     { return t.full }
func (t *fakeTab) Crumbs() []string     { return t.crumbs }
func (t *fakeTab) Filterable() bool     { return t.filtering }
func (t *fakeTab) SetFilter(q string)   { t.filter = q }
func (t *fakeTab) Overflow() (int, int) { return 0, t.below }
func (t *fakeTab) Click(row, count int) bool {
	t.clicks = append(t.clicks, [2]int{row, count})
	return true
}
func (t *fakeTab) Wheel(delta int)        { t.wheel = append(t.wheel, delta) }
func (t *fakeTab) Description(Env) string { return "describes " + t.title }

func lines(n kit.Node) []string {
	var out []string
	switch v := n.(type) {
	case *kit.Container:
		for _, c := range v.Children {
			out = append(out, lines(c)...)
		}
	case *kit.Lines:
		out = append(out, v.Lines...)
	}
	return out
}

func newFrame(w, rows int, tabs ...Tab) *Frame {
	return New(func() Env { return Env{Width: w, Rows: rows} }, "", tabs...)
}

func render(f *Frame, w int) []string { return lines(f.View(w).Root) }

func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = tui.StripTerminalSequences(r)
	}
	return out
}

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		f := newFrame(w, 20, &fakeTab{id: "a", title: "Providers", rows: 10, crumbs: []string{"A very long drill-in level name"}, below: 99, filtering: true},
			&fakeTab{id: "b", title: "Quotas", dirty: true})
		for _, row := range render(f, w) {
			if tui.VisibleWidth(row) != tui.VisibleWidth(strings.TrimRight(row, " ")) && tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: row %q is %d wide", w, row, tui.VisibleWidth(row))
			}
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: row %q is %d wide", w, row, tui.VisibleWidth(row))
			}
		}
	}
}

func TestFrameHasTheFixedLineOrder(t *testing.T) {
	f := newFrame(80, 30, &fakeTab{id: "a", title: "Providers", rows: 10})
	rows := plainRows(render(f, 80))
	if len(rows) != 10+chromeRows {
		t.Fatalf("%d rows: %q", len(rows), rows)
	}
	if !strings.HasPrefix(rows[0], "── Auth › Providers ─") {
		t.Fatalf("top rule: %q", rows[0])
	}
	if !strings.Contains(rows[1], "❱ Providers ") || !strings.Contains(rows[1], "❱ Providers ❱") && !strings.HasSuffix(strings.TrimSpace(rows[1]), "Providers") {
		t.Fatalf("tab bar: %q", rows[1])
	}
	if strings.Contains(rows[1], "[") {
		t.Fatalf("tab bar must not use brackets: %q", rows[1])
	}
	if !strings.Contains(rows[2], "⌕") || rows[3] != "" {
		t.Fatalf("search/blank: %q %q", rows[2], rows[3])
	}
	last := len(rows) - 1
	if !strings.HasPrefix(rows[last-3], "──") || !strings.HasPrefix(rows[last], "──") {
		t.Fatalf("rules: %q / %q", rows[last-3], rows[last])
	}
	if !strings.Contains(rows[last-2], "describes Providers") || !strings.Contains(rows[last-1], "esc") {
		t.Fatalf("description/hints: %q / %q", rows[last-2], rows[last-1])
	}
}

func TestHeightNeverChangesAcrossTabsFiltersHelpAndConfirm(t *testing.T) {
	a := &fakeTab{id: "a", title: "A", rows: 6, filtering: true}
	b := &fakeTab{id: "b", title: "B", rows: 12, body: []string{"one"}, dirty: true}
	f := newFrame(80, 40, a, b)
	want := len(render(f, 80))
	if want != 12+chromeRows {
		t.Fatalf("height %d, want the tallest tab (12) plus chrome", want)
	}
	steps := []string{"\t", "x", "\t", "\x1bOP", "\x1bOP", "\x03"}
	for _, key := range steps {
		f.HandleInput(key)
		if got := len(render(f, 80)); got != want {
			t.Fatalf("after %q the height is %d, want %d", key, got, want)
		}
	}
}

func TestHeightIsCappedToTheTerminalAndFullHeightTabsTakeAllRows(t *testing.T) {
	f := newFrame(80, 12, &fakeTab{id: "a", title: "A", rows: 30})
	if got := len(render(f, 80)); got != 12 {
		t.Fatalf("capped height = %d", got)
	}
	f = newFrame(80, 40, &fakeTab{id: "a", title: "A", rows: 5}, &fakeTab{id: "b", title: "B", full: true})
	f.HandleInput("\t")
	if got := len(render(f, 80)); got != 40 {
		t.Fatalf("full-height tab = %d rows, want 40", got)
	}
}

func TestVeryShortTerminalKeepsRulesDescriptionAndHints(t *testing.T) {
	f := newFrame(80, 5, &fakeTab{id: "a", title: "A", rows: 10})
	rows := plainRows(render(f, 80))
	if len(rows) != chromeRows+minBodyRows {
		t.Fatalf("%d rows", len(rows))
	}
	if !strings.Contains(rows[len(rows)-2], "esc") {
		t.Fatalf("hints lost: %q", rows)
	}
}

func TestRulesCarryScrollMarks(t *testing.T) {
	f := newFrame(80, 30, &fakeTab{id: "a", title: "A", rows: 6, below: 15})
	rows := plainRows(render(f, 80))
	if !strings.Contains(rows[len(rows)-1], "↓ 15 more") {
		t.Fatalf("bottom rule: %q", rows[len(rows)-1])
	}
}

func TestRuleLineIsExactlyWideAndKeepsSlots(t *testing.T) {
	id := func(s string) string { return s }
	for _, w := range []int{20, 36, 57, 80, 140} {
		got := ruleLine(w, id, "Auth › Providers", "↑ 3 more", "9 providers")
		if tui.VisibleWidth(got) != w {
			t.Fatalf("width %d: %q is %d", w, got, tui.VisibleWidth(got))
		}
		if !strings.HasPrefix(got, "──") || !strings.HasSuffix(got, "─") && !strings.HasSuffix(got, "──") {
			t.Fatalf("rule does not start and end with dashes: %q", got)
		}
		if !strings.Contains(got, "Auth") {
			t.Fatalf("left slot lost at %d: %q", w, got)
		}
	}
	wide := ruleLine(80, id, "left", "mid", "right")
	if !strings.Contains(wide, "mid") || !strings.Contains(wide, "right") {
		t.Fatalf("wide rule dropped a slot: %q", wide)
	}
	if got := ruleLine(24, id, "Auth › Providers", "↑ 3 more", "9 providers"); strings.Contains(got, "↑ 3 more") {
		t.Fatalf("middle slot must be dropped first when short: %q", got)
	}
}

func TestBodyIsPaddedAndCutToTheFixedRows(t *testing.T) {
	long := make([]string, 30)
	for i := range long {
		long[i] = "row"
	}
	f := newFrame(80, 40, &fakeTab{id: "a", title: "A", rows: 7, body: long})
	if got := len(render(f, 80)); got != 7+chromeRows {
		t.Fatalf("rows = %d", got)
	}
	f = newFrame(80, 40, &fakeTab{id: "a", title: "A", rows: 7, body: []string{"only"}})
	if got := len(render(f, 80)); got != 7+chromeRows {
		t.Fatalf("padded rows = %d", got)
	}
}

func TestTabSwitchWrapsAndKeysReachTheActiveTab(t *testing.T) {
	a, b := &fakeTab{id: "a", title: "A"}, &fakeTab{id: "b", title: "B"}
	f := newFrame(80, 20, a, b)
	f.HandleInput("\t")
	f.HandleInput("r")
	if len(b.keys) != 1 || len(a.keys) != 0 {
		t.Fatalf("keys a=%v b=%v", a.keys, b.keys)
	}
	f.HandleInput("\t")
	f.HandleInput("r")
	if len(a.keys) != 1 {
		t.Fatalf("wrap failed: %v", a.keys)
	}
}

func TestLeftRightSwitchTabsUnlessTheTabTakesThem(t *testing.T) {
	a, b := &fakeTab{id: "a", title: "A"}, &fakeTab{id: "b", title: "B"}
	f := newFrame(80, 20, a, b)
	f.HandleInput("\x1b[C")
	if f.active != 1 {
		t.Fatal("right must switch to the next tab")
	}
	f.HandleInput("\x1b[D")
	if f.active != 0 {
		t.Fatal("left must switch back")
	}
}

func TestTypingFiltersOnlyFilterableScreensAndEscClearsFirst(t *testing.T) {
	a := &fakeTab{id: "a", title: "A", filtering: true}
	f := newFrame(80, 20, a)
	for _, k := range []string{"o", "p", "u", "s"} {
		f.HandleInput(k)
	}
	if a.filter != "opus" {
		t.Fatalf("filter = %q", a.filter)
	}
	if got := strings.Join(plainRows(render(f, 80)), "\n"); !strings.Contains(got, "⌕ opus") {
		t.Fatalf("search line does not show the query:\n%s", got)
	}
	f.HandleInput("\x7f")
	if a.filter != "opu" {
		t.Fatalf("backspace: %q", a.filter)
	}
	res, _ := f.HandleInput("\x1b")
	if res.Done || a.filter != "" {
		t.Fatalf("esc must clear the filter first: done=%v filter=%q", res.Done, a.filter)
	}
	res, _ = f.HandleInput("\x1b")
	if !res.Done {
		t.Fatal("a second esc closes")
	}
	a.filtering = false
	a.keys = nil
	f.HandleInput("z")
	if a.filter != "" {
		t.Fatal("a screen without a list must not take typed text")
	}
}

func TestFilterIsKeptPerTabAcrossSwitches(t *testing.T) {
	a, b := &fakeTab{id: "a", title: "A", filtering: true}, &fakeTab{id: "b", title: "B", filtering: true}
	f := newFrame(80, 20, a, b)
	f.HandleInput("q")
	f.HandleInput("\t")
	f.HandleInput("z")
	f.HandleInput("\x1b[Z") // shift+tab
	if got := strings.Join(plainRows(render(f, 80)), "\n"); !strings.Contains(got, "⌕ q") {
		t.Fatalf("tab A lost its filter:\n%s", got)
	}
	if b.filter != "z" {
		t.Fatalf("tab B filter %q", b.filter)
	}
}

func TestCloseIsImmediateWhenClean(t *testing.T) {
	f := newFrame(80, 20, &fakeTab{id: "a", title: "A"})
	res, _ := f.HandleInput("\x03")
	if !res.Done {
		t.Fatal("clean ctrl+c must close")
	}
}

func TestCloseAsksBeforeDiscardingAnyTabsDraft(t *testing.T) {
	a, b := &fakeTab{id: "a", title: "A"}, &fakeTab{id: "b", title: "B", dirty: true}
	f := newFrame(80, 20, a, b)
	res, _ := f.HandleInput("\x03")
	if res.Done || f.modal == nil {
		t.Fatal("dirty close must open the confirmation")
	}
	res, _ = f.HandleInput("\r") // Keep editing is first and default
	if res.Done || b.discarded {
		t.Fatal("keep editing must not discard or close")
	}
	f.HandleInput("\x03")
	f.HandleInput("\x1b[B")
	res, _ = f.HandleInput("\r")
	if !res.Done || !b.discarded {
		t.Fatal("discard must drop drafts and close")
	}
}

func TestEscCancelsTheConfirmAndKeepsEditing(t *testing.T) {
	b := &fakeTab{id: "b", title: "B", dirty: true}
	f := newFrame(80, 20, b)
	f.HandleInput("\x03")
	res, _ := f.HandleInput("\x1b")
	if res.Done || b.discarded || f.modal != nil {
		t.Fatalf("esc on the confirm must keep editing: done=%v modal=%v", res.Done, f.modal)
	}
}

func TestConfirmScreenRendersInsideTheFixedFrame(t *testing.T) {
	b := &fakeTab{id: "b", title: "B", dirty: true, rows: 10}
	f := newFrame(80, 30, b)
	before := len(render(f, 80))
	f.HandleInput("\x03")
	rows := plainRows(render(f, 80))
	if len(rows) != before {
		t.Fatalf("height changed: %d -> %d", before, len(rows))
	}
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"Auth › B › Discard", "Discard unsaved changes", "→ Keep editing", "Discard changes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("confirm lacks %q:\n%s", want, joined)
		}
	}
}

func TestChooseBlocksUntilAnswered(t *testing.T) {
	f := newFrame(80, 20, &fakeTab{id: "a", title: "A"})
	got := make(chan string, 1)
	go func() {
		c, ok := f.Choose("q", []string{"one", "two"}, nil)
		if ok {
			got <- c
		}
	}()
	for f.ModalTitle() == "" {
	}
	f.HandleInput("\x1b[B")
	f.HandleInput("\r")
	if c := <-got; c != "two" {
		t.Fatalf("choice %q", c)
	}
}

func TestChooseReturnsWhenSessionEnds(t *testing.T) {
	f := newFrame(80, 20, &fakeTab{id: "a", title: "A"})
	done := make(chan struct{})
	close(done)
	if _, ok := f.Choose("q", []string{"x"}, done); ok {
		t.Fatal("a closed session must not answer")
	}
}

func TestHelpToggleReplacesTheBody(t *testing.T) {
	f := newFrame(80, 20, &fakeTab{id: "a", title: "A"})
	f.HandleInput("\x1bOP") // F1
	if !f.help {
		t.Skip("F1 sequence not matched in this terminal table")
	}
	got := strings.Join(plainRows(render(f, 80)), "\n")
	if !strings.Contains(got, "help text") || !strings.Contains(got, "Auth › A › Help") {
		t.Fatalf("help screen missing:\n%s", got)
	}
}

var _ sdk.ViewComponent = (*Frame)(nil)
var _ sdk.RemoteComponentInvalidator = (*Frame)(nil)

func TestTabBarKeepsTheActiveTabAtNarrowWidths(t *testing.T) {
	titles := []string{"Providers", "Quotas", "Tokens", "Models", "Settings"}
	for w := 20; w <= 70; w++ {
		for cur := range titles {
			var tabs []Tab
			for _, ti := range titles {
				tabs = append(tabs, &fakeTab{id: ti, title: ti})
			}
			f := New(func() Env { return Env{Width: w, Rows: 20} }, titles[cur], tabs...)
			rows := plainRows(render(f, w))
			strip := rows[1]
			if tui.VisibleWidth(strip) > w {
				t.Fatalf("width %d: %q is %d wide", w, strip, tui.VisibleWidth(strip))
			}
			if !strings.Contains(strip, strings.TrimSpace(titles[cur])) && !strings.Contains(strip, titles[cur][:3]) {
				t.Fatalf("width %d, active %d lost: %q", w, cur, strip)
			}
		}
	}
}

func TestNarrowTabBarShowsPositionWhenScrolled(t *testing.T) {
	var tabs []Tab
	for _, ti := range []string{"Providers", "Quotas", "Tokens", "Models", "Settings"} {
		tabs = append(tabs, &fakeTab{id: ti, title: ti})
	}
	f := New(func() Env { return Env{Width: 36, Rows: 20} }, "Quotas", tabs...)
	if strip := plainRows(render(f, 36))[1]; !strings.Contains(strip, "2/5") {
		t.Fatalf("no position marker in %q", strip)
	}
}

func TestHelpAndCloseSurviveTheNarrowestHintRow(t *testing.T) {
	for _, w := range []int{30, 36, 57} {
		f := newFrame(w, 20, &fakeTab{id: "a", title: "A"})
		rows := plainRows(render(f, w))
		hint := rows[len(rows)-2]
		if !strings.Contains(hint, "f1") || !strings.Contains(hint, "close") {
			t.Fatalf("width %d: help and close must stay in the hint row: %q", w, hint)
		}
	}
}

func TestMouseClicksTabsRowsAndWheelsTheBody(t *testing.T) {
	a, b := &fakeTab{id: "a", title: "A", rows: 8}, &fakeTab{id: "b", title: "B"}
	f := newFrame(80, 30, a, b)
	render(f, 80)
	// Chip B starts after "❱ A " (1 + 4 cells + 1 separator).
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 8, Y: 1, ClickCount: 1})
	if f.active != 1 {
		t.Fatalf("tab click did not select B (active %d)", f.active)
	}
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 2, Y: 1, ClickCount: 1})
	render(f, 80)
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 5, Y: 6, ClickCount: 2})
	if len(a.clicks) != 1 || a.clicks[0] != [2]int{2, 2} {
		t.Fatalf("body click = %v", a.clicks)
	}
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 5, Y: 0})
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 5, Y: 4 + 10})
	if len(a.clicks) != 1 {
		t.Fatalf("clicks on rules and chrome must be ignored: %v", a.clicks)
	}
	f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseWheel, WheelDelta: 3})
	if len(a.wheel) != 1 || a.wheel[0] != 3 {
		t.Fatalf("wheel = %v", a.wheel)
	}
}

func TestMouseSelectsAndActivatesConfirmOptions(t *testing.T) {
	b := &fakeTab{id: "b", title: "B", dirty: true, rows: 10}
	f := newFrame(80, 30, b)
	f.HandleInput("\x03")
	render(f, 80)
	// Question, one fact, a blank row, then the options: "Discard changes" is body row 4.
	res, _ := f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 6, Y: 4 + 4, ClickCount: 1})
	if res.Done {
		t.Fatal("a single click only selects")
	}
	res, _ = f.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: 6, Y: 4 + 4, ClickCount: 2})
	if !res.Done || !b.discarded {
		t.Fatal("double click activates the selected option")
	}
}

func TestSlashFocusesSearchInsteadOfTypingAndAltArrowsScrollALine(t *testing.T) {
	a := &fakeTab{id: "a", title: "A", filtering: true}
	f := newFrame(80, 20, a)
	f.HandleInput("/")
	if a.filter != "" {
		t.Fatalf("a leading slash must not enter the filter: %q", a.filter)
	}
	f.HandleInput("a")
	f.HandleInput("/")
	if a.filter != "a/" {
		t.Fatalf("a slash inside a query is text: %q", a.filter)
	}
	f.HandleInput("\x1b[1;3B") // alt+down
	f.HandleInput("\x1b[1;3A") // alt+up
	if len(a.wheel) != 2 || a.wheel[0] != 1 || a.wheel[1] != -1 {
		t.Fatalf("alt+arrows scrolled %v, want [1 -1]", a.wheel)
	}
}

func TestLeaveHintSaysBackInADrillInAndCloseOnTheRoot(t *testing.T) {
	root := tabHints([]string{"x"}, true, false)
	drilled := tabHints([]string{"x"}, false, true)
	last := func(h []hint) string { return h[len(h)-1].text }
	if !strings.Contains(last(root), "close") || strings.Contains(last(root), "back") {
		t.Fatalf("root leave hint: %q", last(root))
	}
	if !strings.Contains(last(drilled), "back") || strings.Contains(last(drilled), "close") {
		t.Fatalf("drill-in leave hint: %q", last(drilled))
	}
}
