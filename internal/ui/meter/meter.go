// Package meter draws the used-allowance bar shared by the dashboard's tabs.
package meter

import (
	"math"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// Bar draws cells cells for a used fraction in [0,1] with the stress colour ramp; the filled
// part is never the only signal, callers print the number next to it. A nil fraction is
// unknown and draws dots, never an empty bar that would read as zero.
func Bar(th line.Theme, used *float64, cells int) string {
	if cells <= 0 {
		return ""
	}
	if used == nil {
		return th.Paint(strings.Repeat("·", cells), line.Style{Fg: &line.Color{Token: "dim"}})
	}
	u := math.Min(1, math.Max(0, *used))
	filled := int(math.Round(u * float64(cells)))
	var b strings.Builder
	for i := range cells {
		if i < filled {
			s := float64(i)/float64(max(cells-1, 1))*0.45 + u*0.55
			b.WriteString(th.Paint("█", line.Style{Stress: &s}))
		} else {
			b.WriteString(th.Paint("░", line.Style{Fg: &line.Color{Token: "dim"}}))
		}
	}
	return b.String()
}
