package form

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

type plain struct{}

func (plain) Paint(text string, _ line.Style) string { return text }

func TestLineFitsAndKeepsTheValueBeforeTheSource(t *testing.T) {
	r := Row{ID: "a", Label: "Refresh interval", Value: "120 s", Source: "● staged → user"}
	for _, w := range []int{20, 36, 57, 80} {
		l := Line(plain{}, r, 16, true, NoEdit, w)
		if tui.VisibleWidth(l) > w {
			t.Fatalf("width %d: %q", w, l)
		}
		if w >= 36 && !strings.Contains(l, "120 s") {
			t.Fatalf("width %d lost the value: %q", w, l)
		}
	}
	if l := Line(plain{}, r, 16, false, NoEdit, 80); !strings.HasSuffix(l, "● staged → user") {
		t.Fatalf("source must be right-aligned: %q", l)
	}
}

func TestHeaderRowIsATitleAndNeverSelected(t *testing.T) {
	rows := []Row{{ID: "h", Label: "Quotas", Header: true}, {ID: "a", Label: "A", Value: "1"}}
	var s Screen
	lines, _, _ := s.Draw(plain{}, rows, NoEdit, 5, 40)
	if len(lines) != 2 || strings.Contains(lines[0], "→") || !strings.Contains(lines[1], "→") {
		t.Fatalf("lines %q", lines)
	}
	if s.Index(rows) != 1 {
		t.Fatalf("cursor on %d", s.Index(rows))
	}
}

func TestEditIgnoresKeySequencesInsideTypedText(t *testing.T) {
	var e Edit
	e.Begin("ab")
	e.Type("c\x1b[A\x1b[1;5Cd")
	if got := e.Value(); got != "abcd" {
		t.Fatalf("arrow tails leaked into the text: %q", got)
	}
	e.Type("\x1b[A")
	if got := e.Value(); got != "abcd" {
		t.Fatalf("a lone arrow must change nothing: %q", got)
	}
}

func TestEditBackspaceRemovesTheLastRune(t *testing.T) {
	var e Edit
	e.Begin("añ")
	e.Type("\x7f")
	if got := e.Value(); got != "a" {
		t.Fatalf("got %q", got)
	}
}
