package quotas

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/meter"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

// Display is the part of the settings the panel reads on every render.
type Display struct {
	PercentUsed       bool
	HideSignedOut     bool
	CreditHealthyUSD  float64
	CreditCriticalUSD float64
	// Placement is the configured quota line placement, shown in the top rule.
	Placement string
}

// block is one provider's rendered lines.
type block struct {
	id    string
	lines []string
}

const barCells = 8

// maxLabel keeps a window label next to its bar on wide terminals.
const maxLabel = 26

func (d Display) lineConfig() line.Config {
	return line.Config{PercentUsed: d.PercentUsed, CreditHealthyUSD: d.CreditHealthyUSD, CreditCriticalUSD: d.CreditCriticalUSD}
}

// visible picks the views to show: the solo provider alone, otherwise every provider except
// signed-out ones when they are hidden.
func visible(views []quota.View, solo string, hideSignedOut bool) []quota.View {
	var out []quota.View
	for _, v := range views {
		switch {
		case solo != "":
			if v.Source.ID == solo {
				out = append(out, v)
			}
		case hideSignedOut && v.Snapshot != nil && v.Snapshot.NeedsAuth:
		default:
			out = append(out, v)
		}
	}
	return out
}

// render builds one block per visible provider. It reads cached state only.
func render(env frame.Env, views []quota.View, d Display, selected string, now time.Time) []block {
	th := uitheme.Theme{T: env.Theme}
	out := make([]block, 0, len(views))
	for _, v := range views {
		out = append(out, block{id: v.Source.ID, lines: providerLines(env, th, v, d, v.Source.ID == selected, now)})
	}
	return out
}

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fitLine(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

func providerLines(env frame.Env, th line.Theme, v quota.View, d Display, selected bool, now time.Time) []string {
	w := env.Width
	snap := v.Snapshot
	marker := "  "
	if selected {
		marker = paint(th, "→ ", "accent")
	}
	name := th.Paint(v.Source.Label, line.Style{Bold: true})
	if snap != nil && snap.Tier != "" {
		name += paint(th, " · "+line.Plain(snap.Tier), "muted")
	}
	head := marker + name
	status := stateText(th, v, now)
	// Identity first: the state is the segment that goes when the row is tight.
	var below string
	if status != "" {
		if tui.VisibleWidth(head)+2+tui.VisibleWidth(status) <= w {
			head += strings.Repeat(" ", w-tui.VisibleWidth(head)-tui.VisibleWidth(status)) + status
		} else {
			below = fitLine("    "+status, w)
		}
	}
	lines := []string{fitLine(head, w)}
	if below != "" {
		lines = append(lines, below)
	}
	if snap == nil {
		return lines
	}
	return append(lines, detailLines(th, v, d, w, now)...)
}

// detailLines are the lines under a provider heading: every window with its drainage, the
// balance, reset credits and the error. Its caller has checked that a snapshot exists.
func detailLines(th line.Theme, v quota.View, d Display, w int, now time.Time) []string {
	snap := v.Snapshot
	if snap == nil {
		return nil
	}
	var lines []string
	nowMs := now.UnixMilli()
	metrics := map[string]*quota.Metrics{}
	for i := range v.Drainage {
		h := v.Drainage[i]
		metrics[string(h.Kind)+"\x00"+h.ID] = quota.Compute(&h, nowMs)
	}
	for _, win := range snap.Windows {
		lines = append(lines, windowLine(th, win, d, w, nowMs))
		if m := metrics[string(quota.KindWindow)+"\x00"+win.ID]; m != nil {
			if dl := drainLine(th, m, "%", w, nowMs); dl != "" {
				lines = append(lines, dl)
			}
		}
	}
	if b := snap.Credits; b != nil {
		lines = append(lines, creditLine(th, b, d, w))
		if m := metrics[string(quota.KindCredits)+"\x00credits"]; m != nil {
			if dl := drainLine(th, m, b.Currency, w, nowMs); dl != "" {
				lines = append(lines, dl)
			}
		}
	}
	if snap.ResetCredits != nil {
		lines = append(lines, fitLine(paint(th, fmt.Sprintf("    reset credits: %d", *snap.ResetCredits), "muted"), w))
	}
	if snap.Error != "" {
		lines = append(lines, fitLine(paint(th, "    ▲ "+line.Plain(snap.Error), "warning"), w))
	}
	return lines
}

// stateText is the right-hand status of a provider row: a glyph and a word, never color alone.
func stateText(th line.Theme, v quota.View, now time.Time) string {
	snap := v.Snapshot
	switch {
	case v.Refreshing:
		return paint(th, "↻ refreshing", "accent")
	case snap != nil && snap.NeedsAuth:
		return paint(th, "○ login required: /login "+v.Source.ID, "warning")
	case v.Backoff != nil && v.Backoff.Until > now.UnixMilli():
		wait := time.Duration(v.Backoff.Until-now.UnixMilli()) * time.Millisecond
		text := "▲ retry in " + short(wait)
		if v.Backoff.Status != 0 {
			text += fmt.Sprintf(" (HTTP %d)", v.Backoff.Status)
		}
		return paint(th, text, "warning")
	case snap != nil && snap.Error != "":
		return paint(th, "▲ error", "error")
	case snap == nil:
		return paint(th, "· not fetched yet", "dim")
	case v.FetchedAt > 0:
		return paint(th, "● "+short(now.Sub(time.UnixMilli(v.FetchedAt)))+" ago", "dim")
	}
	return ""
}

func short(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func windowLine(th line.Theme, win quota.Window, d Display, w int, nowMs int64) string {
	label := line.Plain(win.Label)
	if label == "" {
		label = line.Plain(win.ID)
	}
	pct := line.FormatPercent(win.Used, d.lineConfig())
	if win.Used == nil && win.Value != "" {
		pct = line.Plain(win.Value)
	}
	reset := ""
	if cd := line.ResetCountdown(win.ResetAtMs, nowMs); cd != "" {
		reset = "↻ " + cd
	}
	cells := barCells
	if w < 48 {
		cells = 5
	}
	// Label, bar and percentage are the identity of a window; the reset countdown goes first.
	// The countdown column is reserved even when empty so bars line up across rows.
	pctCol := fmt.Sprintf("%4s", pct)
	const resetW = 10
	fixed := 4 + 1 + cells + 1 + tui.VisibleWidth(pctCol)
	showReset := w-fixed-resetW-1 >= 12
	labelW := min(max(w-fixed, 6), maxLabel)
	if showReset {
		labelW = min(w-fixed-resetW, maxLabel)
	}
	label = fitLabel(label, labelW-1)
	row := "    " + label + " " + meter.Bar(th, win.Used, cells) + " " + pctCol
	if showReset && reset != "" {
		row += " " + paint(th, reset, "muted")
	}
	return fitLine(row, w)
}

func creditLine(th line.Theme, b *quota.Balance, d Display, w int) string {
	if b.Remaining == nil {
		return fitLine("    "+paint(th, "balance unknown", "muted"), w)
	}
	s := line.CreditStress(*b.Remaining, d.lineConfig())
	text := th.Paint(fmt.Sprintf("%.2f%s", *b.Remaining, line.Plain(b.Currency)), line.Style{Stress: &s})
	row := "    " + text + paint(th, " remaining", "muted")
	if b.Total != nil {
		row += paint(th, fmt.Sprintf(" of %.2f%s", *b.Total, line.Plain(b.Currency)), "dim")
	}
	if b.Detail != "" {
		row += paint(th, " · "+line.Plain(b.Detail), "dim")
	}
	return fitLine(row, w)
}

func drainLine(th line.Theme, m *quota.Metrics, unit string, w int, nowMs int64) string {
	rate := func(label string, r *quota.Rate) string {
		if r == nil {
			return ""
		}
		v := r.PerHour
		if unit == "%" {
			v *= 100
			return fmt.Sprintf("%s %.1f%%/h", label, v)
		}
		return fmt.Sprintf("%s %.2f%s/h", label, v, line.Plain(unit))
	}
	var parts []string
	for _, p := range []string{rate("window", m.Window), rate("1h", m.H1), rate("24h", m.H24)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if m.ETASec != nil {
		parts = append(parts, "empty in "+short(time.Duration(*m.ETASec*float64(time.Second))))
	} else if m.ResetsBeforeExhaustion {
		parts = append(parts, "resets first")
	}
	return fitLine(paint(th, "      drain "+strings.Join(parts, " · "), "dim"), w)
}

type plainTheme struct{}

func (plainTheme) Paint(text string, _ line.Style) string { return text }

// PlainText renders the visible providers as unstyled text for a headless command. It reads
// cached state only and never fetches.
func PlainText(views []quota.View, d Display, solo string, width int, now time.Time) string {
	shown := visible(views, solo, d.HideSignedOut)
	if len(shown) == 0 {
		return emptyText(solo)
	}
	env := frame.Env{Width: width}
	var out []string
	for i, v := range shown {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, providerLines(env, plainTheme{}, v, d, false, now)...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// fitLabel cuts the middle of a long label so a trailing period such as "(7d)", which tells
// sibling windows apart, survives.
func fitLabel(label string, width int) string {
	if tui.VisibleWidth(label) <= width {
		return tui.TruncateToWidth(label, width, "…", true)
	}
	i := strings.LastIndex(label, " (")
	if i < 0 || !strings.HasSuffix(label, ")") {
		return tui.TruncateToWidth(label, width, "…", false)
	}
	suffix := label[i:]
	room := width - tui.VisibleWidth(suffix)
	if room < 3 {
		return tui.TruncateToWidth(label, width, "…", false)
	}
	return tui.TruncateToWidth(label[:i], room, "…", false) + suffix
}
