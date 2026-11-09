package providers

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func sample(now time.Time) []Row {
	return []Row{
		{ID: "claude", Label: "Claude", Aliases: []string{"cl"}, State: SignedIn, Source: "OAuth login", Expires: now.Add(6*time.Hour + 12*time.Minute), Logout: "/logout claude"},
		{ID: "synthetic", Label: "Synthetic", State: SignedOut, Login: "/login synthetic"},
		{ID: "antigravity", Label: "Antigravity", State: Expired, Source: "OAuth login", Expires: now.Add(-3 * time.Hour), Login: "/login antigravity"},
		{ID: "xai", Label: "xAI \x1b[31mred", State: Unknown, Err: "boom\x1b[2J"},
	}
}

func extra(id string) Extra {
	if id == "claude" {
		return Extra{Plan: "Max 20x", Headline: "5h 6% · 7d 58%"}
	}
	return Extra{}
}

func TestRowsFitEveryWidthAndKeepLabelAndState(t *testing.T) {
	now := time.Now()
	rows := sample(now)
	for _, w := range []int{36, 57, 80, 140} {
		for _, r := range rows {
			l := rowLine(plainTheme{}, r, extra(r.ID), labelWidth(rows), true, w, now)
			if tui.VisibleWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, l, tui.VisibleWidth(l))
			}
			mark, _, _ := glyph(r.State)
			if !strings.Contains(l, mark) {
				t.Fatalf("width %d: state glyph %q lost in %q", w, mark, l)
			}
			if !strings.Contains(l, strings.Fields(r.Label)[0]) {
				t.Fatalf("width %d: label lost in %q", w, l)
			}
		}
	}
}

func TestWideRowCarriesSourcePlanExpiryAndHeadline(t *testing.T) {
	now := time.Now()
	r := sample(now)[0]
	l := rowLine(plainTheme{}, r, extra("claude"), 12, false, 140, now)
	for _, want := range []string{"● ", "OAuth login", "Max 20x", "expires in 6h 12m", "5h 6% · 7d 58%"} {
		if !strings.Contains(l, want) {
			t.Fatalf("missing %q in %q", want, l)
		}
	}
}

func TestStateIsAGlyphAndAWordAndUntrustedTextIsStripped(t *testing.T) {
	now := time.Now()
	text := PlainText(sample(now), extra, 140, now)
	for _, want := range []string{"● ", "◐ token expired", "○ not signed in · /login synthetic"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("untrusted escape sequence survived:\n%q", text)
	}
}

func TestDetailFieldsNameTheProviderCredentialAndLogout(t *testing.T) {
	now := time.Now()
	r := sample(now)[0]
	fields := detailFields(r, Extra{Plan: "Max 20x", Fields: []Field{{Label: "Quota", Value: "5h 6%", Jump: "quotas"}}}, now)
	got := map[string]string{}
	for _, f := range fields {
		got[f.Label] = f.Value
	}
	if !strings.Contains(got["Provider"], "alias cl") || !strings.Contains(got["Credential"], "OAuth login") ||
		got["Plan"] != "Max 20x" || !strings.Contains(got["Sign in/out"], "/logout claude") || got["Quota"] != "5h 6%" {
		t.Fatalf("fields: %+v", got)
	}
}

func TestJumpFieldShowsItsTargetOnlyWhenThereIsRoom(t *testing.T) {
	f := Field{Label: "Quota", Value: "5h 6%", Jump: "quotas"}
	wide := fieldLine(plainTheme{}, f, 8, false, 80)
	if !strings.HasSuffix(wide, "→ open in Quotas") || tui.VisibleWidth(wide) != 80 {
		t.Fatalf("wide: %q (%d)", wide, tui.VisibleWidth(wide))
	}
	if narrow := fieldLine(plainTheme{}, f, 8, false, 24); strings.Contains(narrow, "open in") || tui.VisibleWidth(narrow) > 24 {
		t.Fatalf("narrow: %q", narrow)
	}
}
