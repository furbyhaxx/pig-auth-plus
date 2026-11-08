// Package settings is the Settings dashboard tab: every setting with its effective value and
// source layer, staged edits and an explicit save.
package settings

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

func paint(th line.Theme, text, token string) string {
	return th.Paint(text, line.Style{Fg: &line.Color{Token: token}})
}

func fit(s string, width int) string { return tui.TruncateToWidth(s, width, "…", false) }

// Format renders a normalized setting value on one line.
func Format(d *config.Definition, v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprint(x)
	case string:
		return `"` + line.Plain(strings.Join(strings.Fields(x), " ")) + `"`
	case []string:
		if len(x) == 0 {
			return "(none)"
		}
		return strings.Join(x, ", ")
	case []config.Alias:
		return fmt.Sprintf("%d alias(es)", len(x))
	case []config.ReplacePattern:
		return fmt.Sprintf("%d rule(s)", len(x))
	}
	return fmt.Sprint(v)
}

// row is one setting as displayed.
type row struct {
	def    *config.Definition
	value  any
	source string
	staged bool
	reset  bool
}

// rowLines renders one setting: the key and its value on one row when they fit, otherwise
// the value below. Staged rows carry a glyph and a word, never color alone.
func rowLines(th line.Theme, r row, selected bool, width int) []string {
	marker := "  "
	if selected {
		marker = paint(th, "→ ", "accent")
	}
	key := th.Paint(r.def.Key, line.Style{Bold: selected})
	value := Format(r.def, r.value)
	source := r.source
	if r.staged {
		source = "● staged"
		if r.reset {
			source = "● staged reset"
		}
		source = paint(th, source, "warning")
	} else {
		source = paint(th, source, "muted")
	}
	head := marker + key
	tail := value + "  " + source
	if tui.VisibleWidth(head)+2+tui.VisibleWidth(tail) <= width {
		pad := width - tui.VisibleWidth(head) - tui.VisibleWidth(tail)
		return []string{head + strings.Repeat(" ", pad) + tail}
	}
	return []string{fit(head, width), fit("    "+value+"  "+source, width)}
}

// PlainText lists every setting with its value and source for a surface without a TUI.
func PlainText(snap *config.Snapshot, width int, only string) string {
	var b strings.Builder
	for _, d := range config.Catalog {
		if only != "" && d.Key != only {
			continue
		}
		d := d
		fmt.Fprintf(&b, "%s = %s  (%s)\n", d.Key, Format(&d, snap.Value(d.Key)), snap.Source(d.Key))
	}
	for _, w := range snap.Warnings {
		fmt.Fprintf(&b, "▲ %s\n", w)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		out = append(out, fit(l, max(width, 20)))
	}
	return strings.Join(out, "\n")
}
