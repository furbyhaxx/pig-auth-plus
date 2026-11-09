package list

import (
	"fmt"
	"testing"
)

func entries(n int) []Entry {
	out := make([]Entry, n)
	for i := range out {
		out[i] = Entry{ID: fmt.Sprintf("id%d", i), Search: fmt.Sprintf("provider number %d", i)}
	}
	return out
}

func TestCursorFollowsMovesAndTheWindowScrollsToKeepItVisible(t *testing.T) {
	var l List
	es := entries(20)
	l.Window(es, 5)
	for range 7 {
		l.Move(1)
	}
	w := l.Window(es, 5)
	if l.Selected() != "id7" || w.Selected < 0 || w.Entries[w.Selected].ID != "id7" {
		t.Fatalf("selected %q, window %+v", l.Selected(), w)
	}
	if w.Above != 3 || w.Below != 12 {
		t.Fatalf("above/below = %d/%d", w.Above, w.Below)
	}
}

func TestCursorStaysOnItsEntryWhenTheListChanges(t *testing.T) {
	var l List
	es := entries(10)
	l.Window(es, 5)
	l.Select("id4")
	reordered := append([]Entry{{ID: "new"}}, es...)
	w := l.Window(reordered, 5)
	if l.Selected() != "id4" || w.Entries[w.Selected].ID != "id4" {
		t.Fatalf("cursor jumped to %q", l.Selected())
	}
}

func TestFilteringNarrowsAndResetsTheCursor(t *testing.T) {
	var l List
	es := entries(15)
	l.Window(es, 5)
	l.Move(9)
	l.SetFilter("number 1")
	w := l.Window(es, 5)
	if w.Total == 0 || w.Total >= 15 {
		t.Fatalf("filter kept %d of 15", w.Total)
	}
	if w.Selected != 0 {
		t.Fatalf("cursor must start on the best match, got row %d", w.Selected)
	}
	l.SetFilter("zzzzqq")
	w = l.Window(es, 5)
	if w.Total != 0 || w.Selected != -1 || l.Selected() != "" {
		t.Fatalf("empty result must have no cursor: %+v %q", w, l.Selected())
	}
}

func TestMoveStopsAtTheEnds(t *testing.T) {
	var l List
	es := entries(3)
	l.Window(es, 5)
	l.Move(-5)
	if l.Selected() != "id0" {
		t.Fatalf("top: %q", l.Selected())
	}
	l.Move(99)
	if l.Selected() != "id2" {
		t.Fatalf("bottom: %q", l.Selected())
	}
}

func TestClickSelectsThenReportsASecondClick(t *testing.T) {
	var l List
	es := entries(10)
	l.Window(es, 5)
	if id, again := l.Click(2); id != "id2" || again {
		t.Fatalf("first click: %q %v", id, again)
	}
	if id, again := l.Click(2); id != "id2" || !again {
		t.Fatalf("second click: %q %v", id, again)
	}
	if id, _ := l.Click(9); id != "" {
		t.Fatalf("click past the window must be ignored, got %q", id)
	}
}

func TestScrollTakesTheCursorAlongAndStaysInRange(t *testing.T) {
	var l List
	es := entries(20)
	l.Window(es, 5)
	l.Scroll(3)
	w := l.Window(es, 5)
	if w.Above != 3 || w.Selected < 0 {
		t.Fatalf("scroll: %+v", w)
	}
	l.Scroll(100)
	w = l.Window(es, 5)
	if w.Below != 0 || w.Above != 15 {
		t.Fatalf("scroll past the end: %+v", w)
	}
	l.Scroll(-100)
	if w = l.Window(es, 5); w.Above != 0 {
		t.Fatalf("scroll past the start: %+v", w)
	}
}
