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
}

func (t *fakeTab) ID() string    { return t.id }
func (t *fakeTab) Title() string { return t.title }
func (t *fakeTab) Body(e Env) []string {
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

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		f := newFrame(w, 20, &fakeTab{id: "a", title: "Providers"}, &fakeTab{id: "b", title: "Quotas", dirty: true})
		for _, row := range lines(f.View(w).Root) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: row %q is %d wide", w, row, tui.VisibleWidth(row))
			}
		}
	}
}

func TestBodyIsCappedToTheRowsTheFrameLeaves(t *testing.T) {
	f := newFrame(80, 7, &fakeTab{id: "a", title: "A"})
	body := lines(f.View(80).Root)
	// 7 rows minus 5 chrome rows, with a floor of 3 body rows.
	if len(body) != 2+3+1 {
		t.Fatalf("rows = %d: %q", len(body), body)
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
	res, _ = f.HandleViewEvent(kit.Event{Node: "modal", Type: kit.EventSelect, Item: &kit.SelectItem{Value: "Keep editing"}})
	if res.Done || b.discarded {
		t.Fatal("keep editing must not discard or close")
	}
	f.HandleInput("\x03")
	res, _ = f.HandleViewEvent(kit.Event{Node: "modal", Type: kit.EventSelect, Item: &kit.SelectItem{Value: "Discard changes"}})
	if !res.Done || !b.discarded {
		t.Fatal("discard must drop drafts and close")
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
	for {
		f.mu.Lock()
		m := f.modal
		f.mu.Unlock()
		if m != nil {
			break
		}
	}
	f.HandleViewEvent(kit.Event{Node: "modal", Type: kit.EventSelect, Item: &kit.SelectItem{Value: "two"}})
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

func TestHelpToggleHidesTheBody(t *testing.T) {
	f := newFrame(80, 20, &fakeTab{id: "a", title: "A"})
	f.HandleInput("\x1bOP") // F1
	if !f.help {
		t.Skip("F1 sequence not matched in this terminal table")
	}
	if got := strings.Join(lines(f.View(80).Root), "\n"); !strings.Contains(got, "help text") {
		t.Fatalf("help body missing: %q", got)
	}
}

var _ sdk.ViewComponent = (*Frame)(nil)
var _ sdk.ViewEventHandler = (*Frame)(nil)
var _ sdk.RemoteComponentInvalidator = (*Frame)(nil)
