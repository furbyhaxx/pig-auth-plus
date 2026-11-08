package tokens

import (
	"strings"
	"unicode/utf16"
)

// Kind shapes text for estimation: JSON packs fewer characters into a token than prose.
type Kind int

// Text kinds.
const (
	Prose Kind = iota
	JSON
)

// Ratios are characters per token for prose and JSON.
type Ratios struct{ Prose, JSON float64 }

// DefaultRatios apply to a route without measurements.
var DefaultRatios = Ratios{Prose: 4, JSON: 3}

// KindOf classifies text by its first character.
func KindOf(text string) Kind {
	t := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return JSON
	}
	return Prose
}

// Estimate rounds up so non-empty text never counts as zero. Length is measured in UTF-16 code
// units, matching the host's own estimate, so the two stay comparable.
func (r Ratios) Estimate(text string, k Kind) Count {
	per := r.Prose
	if k == JSON {
		per = r.JSON
	}
	n := len(utf16.Encode([]rune(text)))
	return Count{Tokens: int(float64(n)/per + 0.999999999), Source: SourceEstimate}
}

// RatiosFor selects the measured ratios for a provider route. Claude-on-Google reuses the
// Anthropic route's prose ratio; routes without measurements use DefaultRatios.
func RatiosFor(provider, model string) Ratios {
	switch provider {
	case "claude":
		return Ratios{Prose: 2.73, JSON: 2.7}
	case "antigravity":
		switch {
		case strings.Contains(model, "claude"):
			return Ratios{Prose: 2.73, JSON: 2.08}
		case strings.Contains(model, "gpt-oss"):
			return Ratios{Prose: 4, JSON: 2.75}
		}
		return Ratios{Prose: 4.07, JSON: 2.88}
	}
	return DefaultRatios
}
