package models

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/form"
)

// tailText is the right-hand secondary text of a catalog row.
func tailText(m Model) string {
	var parts []string
	if m.AliasOf != "" {
		parts = append(parts, "alias")
	}
	if len(m.Overridden) > 0 {
		parts = append(parts, "override")
	}
	parts = append(parts, Tokens(m.ContextWindow))
	return strings.Join(parts, " · ")
}

// row renders one catalog row. The model's key survives every width; the display name goes
// first, then the tags, then the context size.
func row(th line.Theme, m Model, selected bool, keyW, width int) string {
	marker := "  "
	if selected {
		marker = form.Paint(th, "→ ", "accent")
	}
	room := width - 2
	keyW = min(keyW, room*55/100)
	key := line.Plain(m.Key())
	if tui.VisibleWidth(key) > room {
		key = midCut(key, room)
	}
	style := line.Style{Bold: selected}
	head := marker + th.Paint(key, style)
	name := line.Plain(m.Label())
	if name == line.Plain(m.ID) {
		name = ""
	}
	tails := []string{tailText(m), Tokens(m.ContextWindow), ""}
	for _, tail := range tails {
		w := tui.VisibleWidth(key)
		if name != "" && tail != "" {
			col := min(keyW, room-tui.VisibleWidth(tail)-12)
			nameRoom := room - max(col, w) - 2 - 2 - tui.VisibleWidth(tail)
			if col >= w && nameRoom >= 8 {
				l := marker + th.Paint(form.Pad(key, col), style) + "  " + form.Paint(th, form.Fit(name, nameRoom), "muted")
				gap := width - tui.VisibleWidth(l) - tui.VisibleWidth(tail)
				return l + strings.Repeat(" ", gap) + form.Paint(th, tail, "muted")
			}
		}
		if tail != "" && w+2+tui.VisibleWidth(tail) <= room {
			gap := room - w - tui.VisibleWidth(tail)
			return head + strings.Repeat(" ", gap) + form.Paint(th, tail, "muted")
		}
	}
	return form.Fit(head, width)
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "–"
	}
	return line.Plain(s)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// PlainText counts the signed-in models per provider for a surface without a TUI.
func PlainText(models []Model, width int) string {
	counts := map[string]int{}
	out := map[string]bool{}
	n := 0
	for _, m := range models {
		if !m.SignedIn {
			out[m.Provider] = true
			continue
		}
		counts[m.Provider]++
		n++
	}
	names := make([]string, 0, len(counts))
	for p := range counts {
		names = append(names, p)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d models in %d signed-in providers\n", n, len(names))
	for _, p := range names {
		fmt.Fprintf(&b, "%s  %d\n", p, counts[p])
	}
	if len(out) > 0 {
		left := make([]string, 0, len(out))
		for p := range out {
			left = append(left, p)
		}
		sort.Strings(left)
		fmt.Fprintf(&b, "not signed in, left out: %s\n", strings.Join(left, ", "))
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		lines = append(lines, form.Fit(l, max(width, 20)))
	}
	return strings.Join(lines, "\n")
}

// midCut shortens s to width columns keeping its start and end.
func midCut(s string, width int) string {
	if tui.VisibleWidth(s) <= width || width < 3 {
		return form.Fit(s, width)
	}
	r := []rune(s)
	tail := width / 3
	head := width - tail - 1
	for tui.VisibleWidth(string(r[:head]))+1+tui.VisibleWidth(string(r[len(r)-tail:])) > width && tail > 1 {
		tail--
	}
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
