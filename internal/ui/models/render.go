package models

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// row renders one catalog row: identity first, then limits when they fit.
func row(th line.Theme, m Model, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = paint(th, "→ ", "accent")
	}
	id := line.Plain(m.ID)
	head := marker + th.Paint(id, line.Style{Bold: selected})
	if tui.VisibleWidth(head) > width {
		// Model ids differ at the tail (dates, versions), so cut the middle.
		head = marker + th.Paint(midCut(id, width-tui.VisibleWidth(marker)), line.Style{Bold: selected})
	}
	var tags []string
	if m.AliasOf != "" {
		tags = append(tags, "alias")
	}
	if len(m.Overridden) > 0 {
		tags = append(tags, "override")
	}
	tail := Tokens(m.ContextWindow)
	if len(tags) > 0 {
		tail = strings.Join(tags, ",") + "  " + tail
	}
	if tui.VisibleWidth(head)+2+tui.VisibleWidth(tail) <= width {
		pad := width - tui.VisibleWidth(head) - tui.VisibleWidth(tail)
		return head + strings.Repeat(" ", pad) + paint(th, tail, "muted")
	}
	return fit(head, width)
}

// listLines renders the filtered catalog grouped by provider; idx maps a model's position in
// shown to its line, for scrolling.
func listLines(th line.Theme, shown []Model, sel, width int) (lines []string, at []int) {
	at = make([]int, len(shown))
	last := ""
	for i, m := range shown {
		if m.Provider != last {
			last = m.Provider
			lines = append(lines, fit(" "+th.Paint(line.Plain(m.Provider), line.Style{Bold: true}), width))
		}
		at[i] = len(lines)
		lines = append(lines, row(th, m, i == sel, width))
	}
	return lines, at
}

// detailLines renders every field of one model; long values wrap rather than clip.
func detailLines(th line.Theme, m Model, width int) []string {
	var out []string
	field := func(name, value string) {
		for i, l := range tui.WrapTextWithAnsi(fmt.Sprintf("%-10s %s", name, value), max(width-2, 10)) {
			if i > 0 {
				l = "           " + strings.TrimLeft(l, " ")
			}
			out = append(out, " "+l)
		}
	}
	out = append(out, " "+th.Paint(m.Label(), line.Style{Bold: true}))
	field("id", m.Key())
	if m.AliasOf != "" {
		field("alias of", m.Provider+"/"+m.AliasOf)
	}
	field("api", orUnknown(m.API))
	field("context", Tokens(m.ContextWindow))
	field("output", Tokens(m.MaxTokens))
	field("reasoning", yesNo(m.Reasoning))
	field("input", orUnknown(strings.Join(m.Input, ", ")))
	field("price/M", fmt.Sprintf("in %s · out %s · cache read %s · cache write %s",
		Price(m.Cost.Input), Price(m.Cost.Output), Price(m.Cost.CacheRead), Price(m.Cost.CacheWrite)))
	if len(m.Overridden) > 0 {
		field("override", strings.Join(m.Overridden, ", "))
	}
	return out
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "?"
	}
	return line.Plain(s)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// PlainText counts models per provider for a surface without a TUI.
func PlainText(models []Model, width int) string {
	counts := map[string]int{}
	for _, m := range models {
		counts[m.Provider]++
	}
	names := make([]string, 0, len(counts))
	for p := range counts {
		names = append(names, p)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d models in %d providers\n", len(models), len(names))
	for _, p := range names {
		fmt.Fprintf(&b, "%s  %d\n", p, counts[p])
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		out = append(out, fit(l, max(width, 20)))
	}
	return strings.Join(out, "\n")
}

// midCut shortens s to width columns keeping its start and end.
func midCut(s string, width int) string {
	if tui.VisibleWidth(s) <= width || width < 3 {
		return fit(s, width)
	}
	r := []rune(s)
	tail := width / 3
	head := width - tail - 1
	for tui.VisibleWidth(string(r[:head]))+1+tui.VisibleWidth(string(r[len(r)-tail:])) > width && tail > 1 {
		tail--
	}
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
