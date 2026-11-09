package models

import (
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/list"
)

// formRow is one row of a field screen: a label, its value and where the value comes from.
type formRow struct {
	id, label, value string
	// source is the right-aligned origin ("provider", "override", "● staged"); token colors it.
	source, token string
	// action rows ("+ New alias") have no label column and no source.
	action bool
}

func labelWidth(rows []formRow) int {
	w := 0
	for _, r := range rows {
		if !r.action {
			w = max(w, tui.VisibleWidth(r.label))
		}
	}
	return w
}

// formLine lays one row out: cursor, label column, value, source. The value keeps its room
// before the source does.
func formLine(th line.Theme, r formRow, labelW int, selected bool, editing string, width int) string {
	marker := "  "
	if selected {
		marker = paint(th, "→ ", "accent")
	}
	if r.action {
		style := line.Style{Fg: &line.Color{Token: "accent"}, Bold: selected}
		return fit(marker+th.Paint(r.label, style), width)
	}
	labelW = min(labelW, max(width/3, 8))
	head := marker + paint(th, pad(fit(r.label, labelW), labelW), "muted") + "  "
	value, source, token := line.Plain(r.value), r.source, r.token
	if selected && editing != "\x00" {
		value, source, token = line.Plain(editing)+"▏", "editing", "accent"
	}
	room := width - tui.VisibleWidth(head)
	if source != "" && room-tui.VisibleWidth(value) >= tui.VisibleWidth(source)+2 {
		gap := room - tui.VisibleWidth(value) - tui.VisibleWidth(source)
		return head + value + strings.Repeat(" ", gap) + paint(th, source, token)
	}
	return fit(head+value, width)
}

// noEdit is the editing argument of formLine for a row that is not being edited.
const noEdit = "\x00"

// screen is the cursor and scroll state of one field screen.
type screen struct{ l list.List }

func entries(rows []formRow) []list.Entry {
	out := make([]list.Entry, len(rows))
	for i, r := range rows {
		out[i] = list.Entry{ID: r.id}
	}
	return out
}

// draw renders the rows that fit in rows lines; editing is the text of the selected row when
// it is being edited, otherwise noEdit.
func (s *screen) draw(th line.Theme, rows []formRow, editing string, height, width int) (lines []string, above, below int) {
	win := s.l.Window(entries(rows), height)
	by := make(map[string]formRow, len(rows))
	for _, r := range rows {
		by[r.id] = r
	}
	lw := labelWidth(rows)
	for i, e := range win.Entries {
		ed := noEdit
		if i == win.Selected {
			ed = editing
		}
		lines = append(lines, formLine(th, by[e.ID], lw, i == win.Selected, ed, width))
	}
	return lines, win.Above, win.Below
}

// index is the position of the selected row in rows, 0 when none is selected.
func (s *screen) index(rows []formRow) int {
	sel := s.l.Selected()
	for i, r := range rows {
		if r.id == sel {
			return i
		}
	}
	return 0
}

// edit is an inline text input; it keeps printable runes only.
type edit struct {
	on   bool
	text []rune
}

func (e *edit) begin(text string) { e.on, e.text = true, []rune(text) }

// type consumes one key of text entry and reports whether it was a text key.
func (e *edit) typeKey(data string) {
	switch {
	case tui.MatchesKey(data, "backspace"):
		if n := len(e.text); n > 0 {
			e.text = e.text[:n-1]
		}
	case tui.MatchesKey(data, "ctrl+u"):
		e.text = nil
	default:
		for _, r := range data {
			if unicode.IsPrint(r) {
				e.text = append(e.text, r)
			}
		}
	}
}

func (e *edit) value() string { return strings.TrimSpace(string(e.text)) }
