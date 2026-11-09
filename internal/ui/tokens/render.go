package tokens

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/meter"
)

// Display carries the settings the tab renders with.
type Display struct {
	// WarningPercent and ErrorPercent colour the context percentage; both are display-only.
	WarningPercent, ErrorPercent int
}

// Breakdown is the full-request split and its state; the zero value means not yet counted.
type Breakdown struct {
	Parts   []tokens.Part
	Loading bool
	Failed  bool
}

const (
	labelWidth = 12
	indent     = "  "
)

var kindOrder = []struct{ kind, label, unit string }{
	{tokens.PartSystem, "system", "message"}, {tokens.PartTools, "tools", "tool"}, {tokens.PartUser, "user", "message"},
	{tokens.PartAssistant, "assistant", "message"}, {tokens.PartToolResult, "tool results", "result"}, {tokens.PartOther, "other", "item"},
}

type plain struct{}

func (plain) Paint(text string, _ line.Style) string { return text }

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// number groups thousands so large counts stay readable.
func number(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// compact shortens a window size: 200000 is "200K", 1048576 is "1M".
func compact(n int) string {
	switch {
	case n >= 1_000_000:
		if n%1_000_000 < 50_000 {
			return fmt.Sprintf("%dM", n/1_000_000)
		}
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// SourceLabel names where a count comes from.
func SourceLabel(s tokens.Source) string {
	switch s {
	case tokens.SourceProvider:
		return "exact (usage)"
	case tokens.SourceEndpoint:
		return "count API"
	default:
		return "estimate"
	}
}

// SourceMark is the glyph that goes with a source; the word always accompanies it where there
// is room, so colour is never the only signal.
func SourceMark(s tokens.Source) (mark, token string) {
	switch s {
	case tokens.SourceProvider:
		return "●", "success"
	case tokens.SourceEndpoint:
		return "◐", "text"
	default:
		return "≈", "warning"
	}
}

func source(th line.Theme, s tokens.Source, withWord bool) string {
	mark, token := SourceMark(s)
	if !withWord {
		return paint(th, mark, token)
	}
	return paint(th, mark+" "+SourceLabel(s), token)
}

func severity(percent float64, d Display) (token, glyph string) {
	switch {
	case d.ErrorPercent > 0 && percent >= float64(d.ErrorPercent):
		return "error", "✗ "
	case d.WarningPercent > 0 && percent >= float64(d.WarningPercent):
		return "warning", "▲ "
	}
	return "text", ""
}

// first returns the first candidate that fits room, else the last cut to size.
func first(room int, candidates ...string) string {
	for _, c := range candidates {
		if tui.VisibleWidth(c) <= room {
			return c
		}
	}
	return fit(candidates[len(candidates)-1], room)
}

func row(th line.Theme, label, value string, width int) string {
	return fit(indent+paint(th, fmt.Sprintf("%-*s", labelWidth, label), "muted")+" "+value, width)
}

func valueRoom(width int) int { return width - len(indent) - labelWidth - 1 }

func bar(th line.Theme, fraction float64, cells int) string {
	return paint(th, "▕", "dim") + meter.Bar(th, &fraction, cells) + paint(th, "▏", "dim")
}

// contextRow is the headline: the bar, the share of the window and the count. The bar and the
// window size go before the count when the row is narrow.
func contextRow(th line.Theme, r *tokens.Report, d Display, room int) string {
	count := th.Paint(number(r.Tokens.Tokens), line.Style{Bold: true})
	if r.Percent == nil {
		return first(room, count+" tokens · context window unknown", count+" tokens", count)
	}
	token, glyph := severity(*r.Percent, d)
	share := paint(th, fmt.Sprintf("%s%.1f%%", glyph, *r.Percent), token)
	frac := *r.Percent / 100
	of := paint(th, " of "+compact(r.ContextWindow), token)
	return first(room,
		bar(th, frac, 10)+" "+share+of+" · "+count+" tokens",
		bar(th, frac, 6)+" "+share+" · "+count+" tokens",
		share+of+" · "+count,
		share+" · "+count,
		count,
	)
}

func piRow(r *tokens.Report, room int) string {
	if r.HostTokens == nil {
		return "not available"
	}
	delta := r.Tokens.Tokens - *r.HostTokens
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	text := number(*r.HostTokens) + " · Δ " + sign + number(delta)
	if *r.HostTokens > 0 {
		return first(room, fmt.Sprintf("%s (%+.1f%%) vs Pi's estimate", text, float64(delta)/float64(*r.HostTokens)*100), text)
	}
	return text
}

// sharePercent is a part's share of the whole breakdown, never of the context window.
func sharePercent(tokens, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(tokens) / float64(total)
}

// partRow lays out one breakdown line: label, count, share bar, share and item count, then
// the source. The source and item count go before the bar.
func partRow(th line.Theme, label, unit string, p tokens.Part, total, width int) string {
	frac := sharePercent(p.Count.Tokens, total)
	nums := fmt.Sprintf("%7s", number(p.Count.Tokens))
	pct := fmt.Sprintf("%3.0f%%", frac*100)
	items := fmt.Sprintf("%d %s", p.Items, unit)
	if p.Items != 1 {
		items += "s"
	}
	head := indent + fmt.Sprintf("%-*s", labelWidth, label) + " " + nums
	bar := " " + bar(th, frac, 8) + " " + pct
	src := source(th, p.Count.Source, true)
	return first(width,
		head+bar+"  "+fmt.Sprintf("%-11s", items)+" "+src,
		head+bar+"  "+items,
		head+bar+" "+source(th, p.Count.Source, false),
		head+" "+pct+" "+source(th, p.Count.Source, false),
		head,
	)
}

// Lines renders the report as rows fitted to width. The count and its source come first;
// detail is what a narrow row loses.
func Lines(th line.Theme, r *tokens.Report, b Breakdown, d Display, width int) []string {
	if r == nil {
		return []string{fit(indent+"No context count yet; it appears once a model is active.", width)}
	}
	room := valueRoom(width)
	out := []string{
		row(th, "Context", contextRow(th, r, d, room), width),
		row(th, "Source", source(th, r.Tokens.Source, true), width),
		row(th, "Pi", piRow(r, room), width),
	}
	if r.Anchor != nil {
		out = append(out, row(th, "Anchor", number(r.Anchor.Tokens)+" · "+source(th, r.Anchor.Source, true), width))
	} else {
		out = append(out, row(th, "Anchor", paint(th, "– none; counted from the full context", "dim"), width))
	}
	out = append(out, row(th, "Tail", first(room,
		fmt.Sprintf("%s · %s · %d messages", number(r.Tail.Tokens), source(th, r.Tail.Source, true), r.TailMessages),
		fmt.Sprintf("%s · %s", number(r.Tail.Tokens), source(th, r.Tail.Source, true)),
		number(r.Tail.Tokens)), width))
	out = append(out, "")
	switch {
	case b.Loading && len(b.Parts) == 0:
		out = append(out, row(th, "Breakdown", paint(th, "fetching…", "dim"), width))
	case b.Failed && len(b.Parts) == 0:
		out = append(out, row(th, "Breakdown", paint(th, "▲ unavailable · alt+r to retry", "warning"), width))
	default:
		out = append(out, row(th, "Breakdown", paint(th, first(room, "full request · share of the request", "full request"), "dim"), width))
		total := 0
		for _, p := range b.Parts {
			total += p.Count.Tokens
		}
		for _, k := range kindOrder {
			for _, p := range b.Parts {
				if p.Kind == k.kind {
					out = append(out, partRow(th, k.label, k.unit, p, total, width))
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range append([]tokens.Count{r.Tokens}, partCounts(b)...) {
		for _, dg := range c.Diagnostics {
			if seen[dg.Message] {
				continue
			}
			seen[dg.Message] = true
			out = append(out, fit(indent+paint(th, "▲ "+line.Plain(dg.Message), "warning"), width))
		}
	}
	return out
}

func partCounts(b Breakdown) []tokens.Count {
	out := make([]tokens.Count, 0, len(b.Parts))
	for _, p := range b.Parts {
		out = append(out, p.Count)
	}
	return out
}

// PlainText renders the latest report for a headless command; it never recounts.
func PlainText(r *tokens.Report, d Display, width int) string {
	if r == nil {
		return "No context count yet; it appears once a model is active."
	}
	return strings.Join(Lines(plain{}, r, Breakdown{}, d, width), "\n")
}
