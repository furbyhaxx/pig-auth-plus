package frame

import (
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Optional tab capabilities. A tab implements only what it needs; the frame supplies a
// neutral default for the rest, so screens can move onto the standard one at a time.

// Crumber is implemented by tabs with drill-in screens: the levels after the tab title.
type Crumber interface{ Crumbs() []string }

// Statuser supplies the short count or state in the top rule's right slot.
type Statuser interface{ Status(env Env) string }

// Describer supplies the one-line description of the selected row or screen.
type Describer interface{ Description(env Env) string }

// Filterer is implemented by tabs whose current screen has a list to filter. The frame owns
// the search line and the typed text and hands every change to SetFilter.
type Filterer interface {
	// Filterable reports whether the current screen takes typed filter text. It is false
	// while the tab edits text itself or shows a screen without a list.
	Filterable() bool
	SetFilter(query string)
}

// Overflower reports how many rows lie above and below the visible window of the body; the
// frame shows them in the top and bottom rule.
type Overflower interface{ Overflow() (above, below int) }

// Sizer declares a tab's natural body height; tabs without it want [DefaultBodyRows].
type Sizer interface {
	BodyRows() int
	// FullHeight marks an inherently large view (a live transcript, a diff) that takes all
	// available rows instead of the shared fixed height.
	FullHeight() bool
}

// Pointer takes mouse input on the body. Row is relative to the first body row.
type Pointer interface {
	// Click selects the row; count is 1 for a click and 2 or more to activate it.
	Click(row, count int) bool
	// Wheel scrolls the body by delta lines; negative is up.
	Wheel(delta int)
}

// DefaultBodyRows is the body height of a tab that declares none.
const DefaultBodyRows = 10

// chromeRows is every line of a frame that is not body: top rule, tab bar, search line, blank
// line, middle rule, description, key hints and bottom rule.
const chromeRows = 8

// minBodyRows keeps a very short terminal usable; rules and hints are never dropped.
const minBodyRows = 2

// ruleLine composes one rule of width w: a left slot, a middle slot centred on the rule and a
// right slot flush to the end, joined by dashes drawn through paint. The slots arrive styled.
// When they do not fit, the middle slot goes first, then the right; the left is cut with an
// ellipsis. The result is exactly w columns wide.
func ruleLine(w int, paint func(string) string, left, mid, right string) string {
	const lead, trail = 2, 2
	slot := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}
	width := func(s string) int { return tui.VisibleWidth(s) }
	l, m, r := slot(left), slot(mid), slot(right)
	total := func() int {
		n := lead + trail + width(l) + width(m) + width(r)
		for _, s := range []string{m, r} {
			if s != "" && (l != "" || (s == r && m != "")) {
				n++
			}
		}
		return n
	}
	if total() > w {
		m = ""
	}
	if total() > w {
		r = ""
	}
	if total() > w && l != "" {
		l = slot(tui.TruncateToWidth(left, max(w-lead-trail-2, 1), "…", false))
	}
	var b strings.Builder
	cursor := 0
	dashesTo := func(to int) {
		if to > cursor {
			b.WriteString(paint(strings.Repeat("─", to-cursor)))
			cursor = to
		}
	}
	emit := func(s string) {
		b.WriteString(s)
		cursor += width(s)
	}
	dashesTo(lead)
	emit(l)
	if m != "" {
		lowest := cursor
		if l != "" {
			lowest++
		}
		highest := w - trail - width(r) - width(m)
		if r != "" {
			highest--
		}
		dashesTo(min(max(w/2-width(m)/2, lowest), max(highest, lowest)))
		emit(m)
	}
	if r != "" {
		dashesTo(w - trail - width(r))
		emit(r)
	}
	dashesTo(w)
	return b.String()
}
