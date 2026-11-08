package line

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Pack joins segments with a dim separator into rows no wider than width; an oversized
// segment wraps. No segment is dropped.
func Pack(th Theme, segments []string, width int) []string {
	if width < 1 {
		return nil
	}
	sep := paintTok(th, " · ", "dim")
	var rows []string
	row := ""
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		joined := seg
		if row != "" {
			joined = row + sep + seg
		}
		if tui.VisibleWidth(joined) <= width {
			row = joined
			continue
		}
		if row != "" {
			rows = append(rows, row)
		}
		wrapped := tui.WrapTextWithAnsi(seg, width)
		if len(wrapped) == 0 {
			row = ""
			continue
		}
		rows = append(rows, wrapped[:len(wrapped)-1]...)
		row = wrapped[len(wrapped)-1]
	}
	if row != "" {
		rows = append(rows, row)
	}
	for i, r := range rows {
		if tui.VisibleWidth(r) > width {
			rows[i] = tui.TruncateToWidth(r, width, "", false)
		}
	}
	return rows
}

// LimitRows keeps editor space: past max rows the last visible row says how many exist.
func LimitRows(th Theme, rows []string, limit, width int) []string {
	limit = max(1, limit)
	if len(rows) <= limit {
		return rows
	}
	note := tui.TruncateToWidth(paintTok(th, fmt.Sprintf("… quota line has %d rows", len(rows)), "dim"), width, "", false)
	return append(append([]string{}, rows[:limit-1]...), note)
}
