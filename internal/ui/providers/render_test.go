package providers

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func sample(now time.Time) []Row {
	return []Row{
		{ID: "claude", Label: "Claude", State: SignedIn, Source: "OAuth", Expires: now.Add(95 * time.Minute)},
		{ID: "synthetic", Label: "Synthetic", State: SignedOut, Login: "/login synthetic"},
		{ID: "antigravity", Label: "Antigravity", State: Expired, Source: "OAuth", Expires: now.Add(-3 * time.Hour), Login: "/login antigravity"},
		{ID: "xai", Label: "xAI \x1b[31mred", State: Unknown, Err: "boom\x1b[2J"},
	}
}

func TestLinesFitEveryWidth(t *testing.T) {
	now := time.Now()
	for _, w := range []int{36, 57, 80, 140} {
		for _, l := range Lines(plainTheme{}, sample(now), "claude", w, now) {
			if tui.VisibleWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, l, tui.VisibleWidth(l))
			}
		}
	}
}

func TestStateIsAWordAndAGlyphAndInputIsStripped(t *testing.T) {
	now := time.Now()
	text := PlainText(sample(now), 140, now)
	for _, want := range []string{"✓ signed in", "○ signed out", "▲ token expired", "token valid 1 h 35 min", "sign in: /login synthetic", "refreshed on use"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("untrusted escape sequence survived:\n%q", text)
	}
}
