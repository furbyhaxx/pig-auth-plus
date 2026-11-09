package uitheme

import (
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// TabSeparator sits before every tab and carries no background, so the tabs read as chips on
// the terminal's own background.
const TabSeparator = "❱"

// greyTokens are tried in order for the inactive tab background: neutral first.
var greyTokens = []string{"toolPendingBg", "selectedBg"}

// greyFallback is the neutral foreground token whose color becomes the chip background when no
// background token is real; terminal-derived themes resolve every *Bg token to the default.
const greyFallback = "borderMuted"

const defaultBg = "\x1b[49m"

// Chrome paints the dashboard frame: the rules in the editor's thinking-level color and the
// tab chips. Every method degrades to plain or modifier-only text when the theme lacks a token.
type Chrome struct {
	t      sdk.UITheme
	level  string
	accent string // background escape for the active tab, empty when unavailable
	accFg  string // contrasting foreground for the accent background
	grey   string // background escape for inactive tabs, empty when unavailable
	greyFg string // foreground for the inactive chips; the muted color on a real background token
}

// NewChrome builds the chrome for a theme and the session's thinking level.
func NewChrome(t sdk.UITheme, level string) Chrome {
	c := Chrome{t: t, level: level}
	mode := tui.TerminalColorMode(t.GetColorMode())
	if col := toTUI(t.Colors()["accent"]); col != nil {
		c.accent = tui.BackgroundAnsi(col, mode)
		c.accFg = tui.ForegroundAnsi(contrastColor(tui.ColorToRgb(col)), mode)
	}
	for _, tok := range greyTokens {
		if bg, err := t.GetBgAnsi(tok); err == nil && bg != "" && bg != defaultBg {
			c.grey = bg
			c.greyFg, _ = t.GetFgAnsi("muted")
			return c
		}
	}
	if col := toTUI(t.Colors()[greyFallback]); col != nil {
		c.grey = tui.BackgroundAnsi(col, mode)
		c.greyFg = tui.ForegroundAnsi(contrastColor(tui.ColorToRgb(col)), mode)
	}
	return c
}

// Rule paints a border segment in the thinking-level color, as the editor border does; when
// the theme has no such token it falls back to the plain border color, then to no color.
func (c Chrome) Rule(text string) string {
	if text == "" {
		return text
	}
	if _, err := c.t.GetFgAnsi(thinkingToken(c.level)); err == nil {
		return c.t.GetThinkingBorderColor(c.level)(text)
	}
	if _, err := c.t.GetFgAnsi("border"); err == nil {
		return c.t.Fg("border", text)
	}
	return text
}

// Tab paints one tab chip. The active tab is bold on the accent background; inactive tabs are
// muted on grey. Without background support the active tab is bold inverse, so it never
// depends on color alone.
func (c Chrome) Tab(title string, active bool) string {
	label := " " + title + " "
	if active {
		if c.accent != "" {
			return tui.StyleTextWithAnsi(label, c.accFg, c.accent, tui.TextAttributes{Bold: true})
		}
		return c.t.Bold(c.t.Inverse(label))
	}
	if c.grey != "" {
		return tui.StyleTextWithAnsi(label, c.greyFg, c.grey, tui.TextAttributes{})
	}
	if _, err := c.t.GetFgAnsi("muted"); err == nil {
		return c.t.Fg("muted", label)
	}
	return label
}

// Strip joins the chips, each preceded by the unstyled separator.
func (c Chrome) Strip(titles []string, active int) string {
	var b strings.Builder
	for i, title := range titles {
		b.WriteString(TabSeparator)
		b.WriteString(c.Tab(title, i == active))
	}
	return b.String()
}

func thinkingToken(level string) string {
	switch level {
	case "minimal", "low", "medium", "high", "xhigh", "max":
		return "thinking" + strings.ToUpper(level[:1]) + level[1:]
	}
	return "thinkingOff"
}

// contrastColor is black on light backgrounds and white on dark ones, by relative luminance.
func contrastColor(bg tui.RgbColor) tui.Color {
	lum := 0.2126*linear(bg.R) + 0.7152*linear(bg.G) + 0.0722*linear(bg.B)
	if lum > 0.179 { // where black and white contrast equally
		return tui.RgbColorValue{R: 0, G: 0, B: 0}
	}
	return tui.RgbColorValue{R: 255, G: 255, B: 255}
}

func linear(c float64) float64 {
	v := c / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}
