package tokens

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
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

const labelWidth = 11

var kindOrder = []struct{ kind, label string }{
	{tokens.PartSystem, "system"}, {tokens.PartTools, "tools"}, {tokens.PartUser, "user"},
	{tokens.PartAssistant, "assistant"}, {tokens.PartToolResult, "tool results"}, {tokens.PartOther, "other"},
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

// totalText picks the fullest wording that fits: the count and its share come before the
// source, and the source before the window size.
func totalText(th line.Theme, r *tokens.Report, d Display, room int) string {
	count := th.Paint(number(r.Tokens.Tokens), line.Style{Bold: true})
	source := SourceLabel(r.Tokens.Source)
	var share, window string
	if r.Percent != nil {
		token, glyph := severity(*r.Percent, d)
		share = paint(th, fmt.Sprintf("%s%.0f%%", glyph, *r.Percent), token)
		window = paint(th, " of "+number(r.ContextWindow), token)
	}
	variants := []string{
		count + " tokens · " + source,
		count + " · " + source,
		count,
	}
	if share != "" {
		variants = []string{
			count + " tokens · " + source + " · " + share + window,
			count + " tokens · " + source + " · " + share,
			count + " · " + share + " · " + source,
			count + " · " + share,
			count,
		}
	}
	for _, v := range variants {
		if tui.VisibleWidth(v) <= room {
			return v
		}
	}
	return variants[len(variants)-1]
}

func row(th line.Theme, label, value string, width int) string {
	return fit(paint(th, fmt.Sprintf("%-*s", labelWidth, label), "muted")+value, width)
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

// Lines renders the report as rows fitted to width. Identity and the count come first; detail
// is what a narrow row loses.
func Lines(th line.Theme, r *tokens.Report, b Breakdown, d Display, width int) []string {
	if r == nil {
		return []string{fit("  No context count yet; it appears once a model is active.", width)}
	}
	var out []string
	out = append(out, row(th, "Total", totalText(th, r, d, width-labelWidth), width))
	out = append(out, row(th, "Context", line.Plain(r.Provider+"/"+r.Model), width))
	if r.HostTokens != nil {
		delta := r.Tokens.Tokens - *r.HostTokens
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		text := number(*r.HostTokens) + " · delta " + sign + number(delta)
		if *r.HostTokens > 0 {
			text += fmt.Sprintf(" (%+.1f%%)", float64(delta)/float64(*r.HostTokens)*100)
		}
		out = append(out, row(th, "Pi", text, width))
	} else {
		out = append(out, row(th, "Pi", paint(th, "not available", "dim"), width))
	}
	if r.Anchor != nil {
		out = append(out, row(th, "Anchor", number(r.Anchor.Tokens)+" · "+SourceLabel(r.Anchor.Source), width))
	} else {
		out = append(out, row(th, "Anchor", paint(th, "none; counted from the full context", "dim"), width))
	}
	out = append(out, row(th, "Tail", fmt.Sprintf("%s · %s · %d messages", number(r.Tail.Tokens), SourceLabel(r.Tail.Source), r.TailMessages), width))
	switch {
	case b.Loading && len(b.Parts) == 0:
		out = append(out, row(th, "Breakdown", paint(th, "counting…", "dim"), width))
	case b.Failed && len(b.Parts) == 0:
		out = append(out, row(th, "Breakdown", paint(th, "▲ unavailable; press r to retry", "warning"), width))
	default:
		out = append(out, row(th, "Breakdown", "", width))
		for _, k := range kindOrder {
			for _, p := range b.Parts {
				if p.Kind != k.kind {
					continue
				}
				text := fmt.Sprintf("%-13s %s · %s · %d", k.label, number(p.Count.Tokens), SourceLabel(p.Count.Source), p.Items)
				out = append(out, fit("  "+text, width))
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
			out = append(out, fit(paint(th, "▲ "+line.Plain(dg.Message), "warning"), width))
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
