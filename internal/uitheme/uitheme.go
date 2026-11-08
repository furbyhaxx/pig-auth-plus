// Package uitheme adapts the host theme to the line package's Theme.
package uitheme

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// Theme paints with a snapshot of the host theme. Text that cannot be styled, because the
// theme lacks the token or the color is unusable, is returned plain rather than dropped.
type Theme struct{ T sdk.UITheme }

var _ line.Theme = Theme{}

// Paint implements line.Theme.
func (t Theme) Paint(text string, s line.Style) string {
	if text == "" {
		return text
	}
	st := sdk.ThemeStyle{TextAttributes: sdk.TextAttributes{
		Bold: s.Bold, Italic: s.Italic, Underline: s.Underline, Strikethrough: s.Strikethrough,
	}}
	var fgAnsi string
	switch {
	case s.Fg != nil:
		if s.Fg.Token != "" {
			st.FgToken = s.Fg.Token
		} else if c, err := tui.ParseColor(s.Fg.Hex); err == nil {
			fgAnsi = tui.ForegroundAnsi(c, tui.TerminalColorMode(t.T.GetColorMode()))
		}
	case s.Stress != nil:
		fgAnsi = t.gradient(*s.Stress)
	}
	if s.Bg != nil {
		if s.Bg.Token != "" {
			st.BgToken = s.Bg.Token
		} else if c, err := tui.ParseColor(s.Bg.Hex); err == nil {
			bg := tui.BackgroundAnsi(c, tui.TerminalColorMode(t.T.GetColorMode()))
			return tui.StyleTextWithAnsi(text, fgAnsi, bg, tui.TextAttributes(st.TextAttributes))
		}
	}
	if fgAnsi != "" {
		bgAnsi := ""
		if st.BgToken != "" {
			bgAnsi, _ = t.T.GetBgAnsi(st.BgToken)
		}
		return tui.StyleTextWithAnsi(text, fgAnsi, bgAnsi, tui.TextAttributes(st.TextAttributes))
	}
	out, err := t.T.Style(text, st)
	if err != nil {
		return text
	}
	return out
}

// gradient is the foreground ANSI for a stress in [0,1]: success to warning to error, mixed
// in OKLCH so the ramp follows the theme.
func (t Theme) gradient(stress float64) string {
	colors := t.T.Colors()
	from, to, amount := colors["success"], colors["warning"], stress*2
	if stress > 0.5 {
		from, to, amount = colors["warning"], colors["error"], (stress-0.5)*2
	}
	a, b := toTUI(from), toTUI(to)
	if a == nil || b == nil {
		return ""
	}
	mixed, err := tui.MixColors(a, b, amount, tui.ColorMixSpaceOklch)
	if err != nil {
		return ""
	}
	return tui.ForegroundAnsi(mixed, tui.TerminalColorMode(t.T.GetColorMode()))
}

func toTUI(c sdk.Color) tui.Color {
	switch c := c.(type) {
	case sdk.IndexedColor:
		return tui.IndexedColor(c)
	case sdk.RgbColorValue:
		return tui.RgbColorValue(c)
	case sdk.OklchColorValue:
		return tui.OklchColorValue(c)
	}
	return nil
}
