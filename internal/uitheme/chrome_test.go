package uitheme

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func TestContrastColorFollowsLuminance(t *testing.T) {
	for _, tc := range []struct {
		bg   tui.RgbColor
		want float64
	}{
		{tui.RgbColor{R: 255, G: 255, B: 255}, 0},
		{tui.RgbColor{R: 20, G: 20, B: 40}, 255},
		{tui.RgbColor{R: 190, G: 120, B: 220}, 0}, // the dark theme's violet accent
	} {
		got := contrastColor(tc.bg).(tui.RgbColorValue)
		if got.R != tc.want {
			t.Errorf("contrast on %v = %v, want %v", tc.bg, got.R, tc.want)
		}
	}
}

func TestThinkingTokenMapsLevels(t *testing.T) {
	for level, want := range map[string]string{
		"off": "thinkingOff", "high": "thinkingHigh", "xhigh": "thinkingXhigh", "": "thinkingOff", "bogus": "thinkingOff",
	} {
		if got := thinkingToken(level); got != want {
			t.Errorf("thinkingToken(%q) = %q, want %q", level, got, want)
		}
	}
}

// A theme without a palette must still produce a usable, marked-active strip.
func TestChromeWithoutPaletteKeepsActiveTabMarked(t *testing.T) {
	c := NewChrome(sdk.UITheme{}, "high")
	if got := c.Rule("──"); got != "──" {
		t.Fatalf("rule without palette = %q", got)
	}
	strip := c.Strip([]string{"Providers", "Quotas"}, 1)
	if !strings.Contains(strip, "\x1b[1m") {
		t.Fatalf("active tab is not bold: %q", strip)
	}
	if plain := tui.StripTerminalSequences(strip); plain != "❱ Providers ❱ Quotas " {
		t.Fatalf("plain strip = %q", plain)
	}
}
