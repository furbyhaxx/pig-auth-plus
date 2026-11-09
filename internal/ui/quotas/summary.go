package quotas

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/meter"
)

// detail levels of a summary row, from most to least informative.
const (
	levelFull     = iota // bar, percentage and reset countdown per window
	levelNoReset         // bar and percentage
	levelSmallBar        // short bar and percentage
	levelPlain           // label and percentage only
	levels
)

func miniBar(th line.Theme, used *float64, cells int) string {
	return paint(th, "▕", "dim") + meter.Bar(th, used, cells) + paint(th, "▏", "dim")
}

// windowSummary is one window of a summary row at the given level.
func windowSummary(th line.Theme, w quota.Window, d Display, level int, nowMs int64) string {
	label := line.Plain(w.Short)
	if label == "" {
		label = line.Plain(w.ID)
	}
	pct := line.FormatPercent(w.Used, d.lineConfig())
	if w.Used == nil && w.Value != "" {
		pct = line.Plain(w.Value)
	}
	switch level {
	case levelPlain:
		return label + " " + pct
	case levelSmallBar:
		return label + " " + miniBar(th, w.Used, 4) + " " + pct
	}
	out := label + " " + miniBar(th, w.Used, barCells) + " " + pct
	if level == levelFull {
		if cd := line.ResetCountdown(w.ResetAtMs, nowMs); cd != "" {
			out += " " + paint(th, "↻"+strings.ReplaceAll(cd, " ", ""), "muted")
		}
	}
	return out
}

func creditSummary(th line.Theme, b *quota.Balance, d Display, level int) string {
	if b.Remaining == nil {
		return paint(th, "balance unknown", "muted")
	}
	s := line.CreditStress(*b.Remaining, d.lineConfig())
	amount := th.Paint(fmt.Sprintf("%s%.2f", line.Plain(b.Currency), *b.Remaining), line.Style{Stress: &s})
	out := amount + " left"
	if level == levelPlain {
		return amount
	}
	if b.Currency == "$" && *b.Remaining <= d.CreditCriticalUSD {
		out += " " + paint(th, "⚠ low", "warning")
	}
	return out
}

// useBalance reports whether the row shows the balance rather than windows: credit sources
// lead with what is left, and their windows are usage detail.
func useBalance(v quota.View) bool {
	s := v.Snapshot
	return s != nil && s.Credits != nil && (v.Source.Credits || len(s.Windows) == 0)
}

// summaryMid is the metric text of a row at a detail level; empty when the provider reports
// nothing and the row shows its state instead.
func summaryMid(th line.Theme, v quota.View, d Display, level int, nowMs int64) string {
	s := v.Snapshot
	if s == nil || s.NeedsAuth {
		return ""
	}
	var parts []string
	if useBalance(v) {
		parts = append(parts, creditSummary(th, s.Credits, d, level))
	} else if len(s.Windows) > 0 {
		for _, w := range line.ImportantWindows(s) {
			parts = append(parts, windowSummary(th, w, d, level, nowMs))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if level == levelFull && s.ResetCredits != nil && *s.ResetCredits > 0 {
		parts = append(parts, fmt.Sprintf("%d reset", *s.ResetCredits))
	}
	return strings.Join(parts, paint(th, " · ", "dim"))
}

// summaryRow lays one provider out on a single line: cursor, label column, metrics, and the
// age or state right-aligned. It reports whether the row fit at the requested level without
// cutting; the caller picks one level for every row so the columns stay aligned.
func summaryRow(th line.Theme, v quota.View, d Display, labelW int, selected bool, w int, now time.Time, level int, withState bool) (string, bool) {
	marker := "  "
	label := line.Plain(v.Source.Label)
	if selected {
		marker = paint(th, "→ ", "accent")
		label = th.Paint(label, line.Style{Bold: true})
	}
	labelW = min(labelW, max(w/3, 6))
	cut := tui.TruncateToWidth(label, labelW, "…", false)
	head := marker + cut + strings.Repeat(" ", max(labelW-tui.VisibleWidth(cut), 0)) + " "
	mid := summaryMid(th, v, d, level, now.UnixMilli())
	right := stateText(th, v, now)
	if mid == "" {
		// Nothing to measure: the state is the row. Its long form goes before it is cut.
		for _, state := range []string{right, shortState(th, v, now)} {
			if l := head + state; tui.VisibleWidth(l) <= w {
				return l, true
			}
		}
		return fitLine(head+shortState(th, v, now), w), false
	}
	l := head + mid
	if withState {
		gap := w - tui.VisibleWidth(l) - tui.VisibleWidth(right)
		if right == "" || gap < 2 {
			return fitLine(l, w), false
		}
		l += strings.Repeat(" ", gap) + right
	}
	return fitLine(l, w), tui.VisibleWidth(l) <= w
}

// summaryRows renders every row at the richest level at which all of them fit, dropping the
// reset countdowns, then the bars, then the age before anything identifying.
func summaryRows(th line.Theme, views []quota.View, d Display, selected int, w int, now time.Time) []string {
	labelW := 0
	for _, v := range views {
		labelW = max(labelW, tui.VisibleWidth(line.Plain(v.Source.Label)))
	}
	render := func(level int, withState bool) ([]string, bool) {
		out := make([]string, len(views))
		ok := true
		for i, v := range views {
			var fit bool
			out[i], fit = summaryRow(th, v, d, labelW, i == selected, w, now, level, withState)
			ok = ok && fit
		}
		return out, ok
	}
	var last []string
	for _, withState := range []bool{true, false} {
		for level := 0; level < levels; level++ {
			rows, ok := render(level, withState)
			if ok {
				return rows
			}
			last = rows
		}
	}
	return last
}

// shortState is the state glyph and word without the long login command, for tight rows.
func shortState(th line.Theme, v quota.View, now time.Time) string {
	if v.Snapshot != nil && v.Snapshot.NeedsAuth {
		return paint(th, "○ login required", "warning")
	}
	return stateText(th, v, now)
}

// describe is the one-line description of a provider row.
func describe(v quota.View, now time.Time, inDetail bool) string {
	s := v.Snapshot
	switch {
	case s != nil && s.NeedsAuth:
		return "Not signed in. Sign in with /login " + v.Source.ID + "; nothing is requested until then."
	case v.Backoff != nil && v.Backoff.Until > now.UnixMilli():
		return fmt.Sprintf("Rate-limited by the provider; the next request is held for %s.", short(time.Duration(v.Backoff.Until-now.UnixMilli())*time.Millisecond))
	case s != nil && s.Error != "":
		return "Last refresh failed: " + line.Plain(s.Error)
	}
	if inDetail {
		return "Every window the provider reports, with observed burn rate and projected exhaustion."
	}
	return "Enter shows every window with burn rate and projected exhaustion. Unknown stays \"?\"."
}
