// Package form is the field-screen toolkit shared by the dashboard tabs: labelled rows with a
// source column, an inline text edit and the cursor/scroll state of one screen.
package form

import (
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/list"
)

// Row is one row of a field screen: a label, its value and where the value comes from. A Header
// row is a section title.
type Row struct {
	ID, Label, Value string
	// Source is the right-aligned origin ("provider", "override", "● staged"); Token colors it.
	Source, Token string
	// Action rows ("+ New alias") have no label column and no source.
	Action bool
	// Header rows are drawn as titles and never selected.
	Header bool
	// Search is extra text the filter matches besides the label and value, such as a key.
	Search string
}

// LabelWidth is the widest label of the rows that have a label column.
func LabelWidth(rows []Row) int {
	w := 0
	for _, r := range rows {
		if !r.Action && !r.Header {
			w = max(w, tui.VisibleWidth(r.Label))
		}
	}
	return w
}

// Line lays one row out: cursor, label column, value, source. The value keeps its room
// before the source does. editing is the text of the row when it is being edited, otherwise
// [NoEdit].
func Line(th line.Theme, r Row, labelW int, selected bool, editing string, width int) string {
	if r.Header {
		return Fit(" "+th.Paint(line.Plain(r.Label), line.Style{Bold: true}), width)
	}
	marker := "  "
	if selected {
		marker = Paint(th, "→ ", "accent")
	}
	if r.Action {
		style := line.Style{Fg: &line.Color{Token: "accent"}, Bold: selected}
		return Fit(marker+th.Paint(r.Label, style), width)
	}
	labelW = min(labelW, max(width/3, 8))
	head := marker + Paint(th, Pad(Fit(r.Label, labelW), labelW), "muted") + "  "
	value, source, token := line.Plain(r.Value), r.Source, r.Token
	if selected && editing != "\x00" {
		value, source, token = line.Plain(editing)+"▏", "editing", "accent"
	}
	room := width - tui.VisibleWidth(head)
	// The origin of a value is part of its meaning, so a long value is cut before it.
	if keep := room - tui.VisibleWidth(source) - 2; source != "" && keep >= min(tui.VisibleWidth(value), minValue) {
		value = Fit(value, keep)
		gap := room - tui.VisibleWidth(value) - tui.VisibleWidth(source)
		return head + value + strings.Repeat(" ", gap) + Paint(th, source, token)
	}
	return Fit(head+value, width)
}

// minValue is the least of a value worth showing next to its source.
const minValue = 6

// NoEdit is the editing argument of Line for a row that is not being edited.
const NoEdit = "\x00"

// Screen is the cursor and scroll state of one field screen.
type Screen struct{ L list.List }

// Entries are the list entries of rows, for a screen that filters or scrolls them itself.
func Entries(rows []Row) []list.Entry {
	out := make([]list.Entry, len(rows))
	for i, r := range rows {
		out[i] = list.Entry{ID: r.ID, Search: r.Label + " " + r.Value + " " + r.Search, Header: r.Header}
	}
	return out
}

// Draw renders the rows that fit in rows lines; editing is the text of the selected row when
// it is being edited, otherwise NoEdit.
func (s *Screen) Draw(th line.Theme, rows []Row, editing string, height, width int) (lines []string, above, below int) {
	win := s.L.Window(Entries(rows), height)
	by := make(map[string]Row, len(rows))
	for _, r := range rows {
		by[r.ID] = r
	}
	lw := LabelWidth(rows)
	for i, e := range win.Entries {
		ed := NoEdit
		if i == win.Selected {
			ed = editing
		}
		lines = append(lines, Line(th, by[e.ID], lw, i == win.Selected, ed, width))
	}
	return lines, win.Above, win.Below
}

// Index is the position of the selected row in rows, 0 when none is selected.
func (s *Screen) Index(rows []Row) int {
	sel := s.L.Selected()
	for i, r := range rows {
		if r.ID == sel {
			return i
		}
	}
	return 0
}

// Edit is an inline text input; it keeps printable runes only.
type Edit struct {
	On   bool
	Text []rune
}

// Begin starts editing text.
func (e *Edit) Begin(text string) { e.On, e.Text = true, []rune(text) }

// Type consumes one key of text entry.
func (e *Edit) Type(data string) {
	switch {
	case tui.MatchesKey(data, "backspace"):
		if n := len(e.Text); n > 0 {
			e.Text = e.Text[:n-1]
		}
	case tui.MatchesKey(data, "ctrl+u"):
		e.Text = nil
	default:
		for _, r := range dropKeySequences(data) {
			if unicode.IsPrint(r) {
				e.Text = append(e.Text, r)
			}
		}
	}
}

// dropKeySequences removes CSI (ESC [ … final) and SS3/Alt (ESC + one rune) sequences: an arrow
// key is not text, and its tail would look printable.
func dropKeySequences(data string) string {
	var b strings.Builder
	rs := []rune(data)
	for i := 0; i < len(rs); i++ {
		if rs[i] != 0x1b {
			b.WriteRune(rs[i])
			continue
		}
		i++
		if i < len(rs) && rs[i] == '[' {
			for i++; i < len(rs) && (rs[i] < 0x40 || rs[i] > 0x7e); i++ {
			}
		}
	}
	return b.String()
}

// Value is the trimmed text.
func (e *Edit) Value() string { return strings.TrimSpace(string(e.Text)) }

// Paint colors text with a theme token.
func Paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

// Fit cuts s to width with an ellipsis.
func Fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// Pad right-pads s with spaces to w columns.
func Pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-tui.VisibleWidth(s), 0))
}
