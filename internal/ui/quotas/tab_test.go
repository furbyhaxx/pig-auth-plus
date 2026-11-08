package quotas

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

var now = time.UnixMilli(1_800_000_000_000)

func ms(d time.Duration) *int64 { v := now.Add(d).UnixMilli(); return &v }

func fixture() []quota.View {
	claude := &quota.Source{ID: "claude", Label: "Claude"}
	or := &quota.Source{ID: "openrouter", Label: "OpenRouter", Credits: true}
	xai := &quota.Source{ID: "xai", Label: "xAI"}
	cx := &quota.Source{ID: "codex", Label: "Codex (ChatGPT)"}
	return []quota.View{
		{Source: claude, FetchedAt: now.Add(-4 * time.Minute).UnixMilli(), Snapshot: &quota.Snapshot{
			Provider: "claude", Tier: "max 20x",
			Windows: []quota.Window{
				{ID: "5h", Label: "5h session", Used: quota.Frac(0.27), ResetAtMs: ms(2 * time.Hour)},
				{ID: "7d", Label: "7d weekly (all models together with a long label)", Used: quota.Frac(0.13), ResetAtMs: ms(100 * time.Hour)},
				{ID: "x", Label: "unknown window"},
			},
			ResetCredits: quota.Int(1),
		}},
		{Source: or, FetchedAt: now.UnixMilli(), Snapshot: &quota.Snapshot{Provider: "openrouter", Credits: &quota.Balance{Remaining: quota.Frac(17.98), Total: quota.Frac(50), Currency: "$"}}},
		{Source: xai, Snapshot: &quota.Snapshot{Provider: "xai", NeedsAuth: true}},
		{Source: cx, Backoff: &quota.Backoff{Until: now.Add(90 * time.Second).UnixMilli(), Status: 429},
			Snapshot: &quota.Snapshot{Provider: "codex", Error: "rate limited \x1b[31mred\x1b[0m"}},
	}
}

func newTab(solo string) *Tab {
	tb := New(fixture, func() Display {
		return Display{PercentUsed: true, HideSignedOut: true, CreditHealthyUSD: 50, CreditCriticalUSD: 5}
	}, Actions{}, solo)
	tb.now = func() time.Time { return now }
	return tb
}

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		for _, row := range newTab("").Body(frame.Env{Width: w, Rows: 40}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: %q is %d wide", w, row, tui.VisibleWidth(row))
			}
		}
	}
}

func TestIdentityAndPrimaryNumbersSurviveNarrowWidth(t *testing.T) {
	body := strings.Join(newTab("").Body(frame.Env{Width: 36, Rows: 40}), "\n")
	for _, want := range []string{"Claude", "max 20x", "27%", "13%", "OpenRouter", "17.98$"} {
		if !strings.Contains(tui.StripTerminalSequences(body), want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}

func TestUnknownIsAQuestionMarkNotZero(t *testing.T) {
	body := tui.StripTerminalSequences(strings.Join(newTab("").Body(frame.Env{Width: 80, Rows: 40}), "\n"))
	for _, row := range strings.Split(body, "\n") {
		if strings.Contains(row, "unknown window") && (!strings.Contains(row, "?") || strings.Contains(row, "0%")) {
			t.Fatalf("unknown window row = %q", row)
		}
	}
}

func TestSignedOutIsHiddenUnlessSolo(t *testing.T) {
	all := tui.StripTerminalSequences(strings.Join(newTab("").Body(frame.Env{Width: 80, Rows: 40}), "\n"))
	if strings.Contains(all, "xAI") {
		t.Fatalf("signed-out provider shown:\n%s", all)
	}
	solo := tui.StripTerminalSequences(strings.Join(newTab("xai").Body(frame.Env{Width: 80, Rows: 40}), "\n"))
	if !strings.Contains(solo, "login required: /login xai") || strings.Contains(solo, "Claude") {
		t.Fatalf("solo xai:\n%s", solo)
	}
}

func TestErrorAndBackoffAreVisibleAndSanitized(t *testing.T) {
	body := tui.StripTerminalSequences(strings.Join(newTab("codex").Body(frame.Env{Width: 80, Rows: 40}), "\n"))
	if !strings.Contains(body, "retry in 1m (HTTP 429)") || !strings.Contains(body, "rate limited red") || strings.Contains(body, "\x1b") {
		t.Fatalf("codex:\n%s", body)
	}
}

func TestKeysDispatchActionsForTheSelectedProvider(t *testing.T) {
	var refreshed, reset []string
	placement := 0
	tb := newTab("")
	tb.actions = Actions{
		Refresh:   func(id string) { refreshed = append(refreshed, id) },
		Placement: func() { placement++ },
		Reset:     func(id string) { reset = append(reset, id) },
	}
	tb.views = func() []quota.View {
		v := fixture()
		v[3].Source.Resets = nopResets{}
		return v
	}
	env := frame.Env{Width: 80, Rows: 40}
	tb.Body(env)
	tb.Key("r")
	tb.Key("R")
	tb.Key("p")
	tb.Key("x") // claude cannot reset: no spend, a notice instead
	if len(reset) != 0 {
		t.Fatalf("reset on a provider without credits: %v", reset)
	}
	for tb.sel != "codex" {
		tb.Key("j")
		tb.Body(env)
	}
	tb.Key("x")
	if want := []string{"claude", ""}; len(refreshed) != 2 || refreshed[0] != want[0] || refreshed[1] != want[1] {
		t.Fatalf("refreshed = %q", refreshed)
	}
	if placement != 1 || len(reset) != 1 || reset[0] != "codex" {
		t.Fatalf("placement=%d reset=%v", placement, reset)
	}
}

func TestSoloTogglesAndNoticeClearsOnNextKey(t *testing.T) {
	tb := newTab("")
	env := frame.Env{Width: 80, Rows: 40}
	tb.Body(env)
	tb.Key("a")
	if got := len(tb.Body(env)); got > 8 {
		t.Fatalf("solo body has %d rows", got)
	}
	tb.Key("a")
	tb.SetNotice("saved", false)
	if body := strings.Join(tb.Body(env), "\n"); !strings.Contains(body, "saved") {
		t.Fatal("notice not shown")
	}
	tb.Key("j")
	if body := strings.Join(tb.Body(env), "\n"); strings.Contains(body, "saved") {
		t.Fatal("notice not cleared by the next key")
	}
}

type nopResets struct{}

func (nopResets) List(context.Context, quota.Env) (quota.ResetList, error) {
	return quota.ResetList{}, nil
}
func (nopResets) Consume(context.Context, quota.Env, string) (quota.ResetOutcome, error) {
	return quota.ResetOutcome{}, nil
}

func TestSelectionMovesAndScrollsTallProviders(t *testing.T) {
	tb := newTab("")
	env := frame.Env{Width: 80, Rows: 4}
	tb.Body(env)
	first := tb.sel
	for i := 0; i < 30 && tb.sel == first; i++ {
		tb.Key("j")
		tb.Body(env)
	}
	if tb.sel == first {
		t.Fatal("selection never advanced")
	}
	for i := 0; i < 60; i++ {
		tb.Key("k")
		tb.Body(env)
	}
	if tb.sel != first || tb.top != 0 {
		t.Fatalf("sel=%s top=%d", tb.sel, tb.top)
	}
}

func TestUsedAndRemainingFollowTheSetting(t *testing.T) {
	tb := New(fixture, func() Display { return Display{PercentUsed: false, HideSignedOut: true} }, Actions{}, "claude")
	tb.now = func() time.Time { return now }
	body := tui.StripTerminalSequences(strings.Join(tb.Body(frame.Env{Width: 80, Rows: 40}), "\n"))
	if !strings.Contains(body, "73%") {
		t.Fatalf("remaining 73%% missing:\n%s", body)
	}
}

func TestSiblingWindowsStayDistinctWhenTheLabelIsCut(t *testing.T) {
	a := fitLabel("Claude and GPT models (7d)", 18)
	b := fitLabel("Claude and GPT models (5h)", 18)
	if a == b || !strings.HasSuffix(a, "(7d)") || !strings.HasSuffix(b, "(5h)") {
		t.Fatalf("%q vs %q", a, b)
	}
	if w := tui.VisibleWidth(a); w > 18 {
		t.Fatalf("width %d", w)
	}
}
