package line

import (
	"fmt"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Config is what the line reads from settings on every render.
type Config struct {
	PercentUsed       bool // true shows used percentages, false remaining
	CreditHealthyUSD  float64
	CreditCriticalUSD float64
	HideSignedOut     bool
	CompactWidthCols  int
	AlwaysShortIDs    bool
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Min(1, math.Max(0, v))
}

// CreditStress maps a known remaining balance to stress in [0,1] using the configured
// healthy and critical thresholds.
func CreditStress(remaining float64, c Config) float64 {
	healthy := math.Max(1, c.CreditHealthyUSD)
	crit := math.Max(0, math.Min(healthy-0.01, c.CreditCriticalUSD))
	switch {
	case remaining <= crit:
		if remaining <= 0 {
			return 1
		}
		return 0.8 + 0.2*(1-remaining/math.Max(0.01, crit))
	case remaining >= healthy:
		return 0
	}
	return clamp01((healthy - remaining) / (healthy - crit) * 0.8)
}

// FormatPercent renders a used fraction in the configured direction; unknown is "?".
func FormatPercent(used *float64, c Config) string {
	if used == nil {
		return "?"
	}
	u := clamp01(*used)
	if !c.PercentUsed {
		u = 1 - u
	}
	return fmt.Sprintf("%d%%", int(math.Round(u*100)))
}

// ResetCountdown renders the time to a reset such as "2d 3h"; empty when unknown.
func ResetCountdown(resetAtMs *int64, nowMs int64) string {
	if resetAtMs == nil || *resetAtMs <= 0 {
		return ""
	}
	sec := (*resetAtMs - nowMs) / 1000
	if sec <= 0 {
		return "now"
	}
	days, hours, mins := sec/86400, sec%86400/3600, sec%3600/60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// Plain strips terminal sequences and control characters from provider-supplied text.
func Plain(text string) string {
	var b strings.Builder
	for _, r := range tui.StripTerminalSequences(text) {
		if r < 32 || (r >= 127 && r <= 159) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
