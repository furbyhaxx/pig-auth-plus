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

func newTab(open string) *Tab {
	tb := New(fixture, func() Display {
		return Display{PercentUsed: true, HideSignedOut: true, CreditHealthyUSD: 50, CreditCriticalUSD: 5, Placement: "below-editor-first"}
	}, Actions{}, open)
	tb.now = func() time.Time { return now }
	return tb
}

func plain(lines []string) string { return tui.StripTerminalSequences(strings.Join(lines, "\n")) }

func TestEveryRowFitsAtAllWidthsOnTheListAndInEveryDetail(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		for _, open := range []string{"", "claude", "openrouter", "xai", "codex"} {
			for _, row := range newTab(open).Body(frame.Env{Width: w, Rows: 40}) {
				if tui.VisibleWidth(row) > w {
					t.Fatalf("width %d open %q: %q is %d wide", w, open, row, tui.VisibleWidth(row))
				}
			}
		}
	}
}

func TestSummaryRowIsOneLineWithIdentityAndHeadlineNumbersAtEveryWidth(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		body := newTab("").Body(frame.Env{Width: w, Rows: 40})
		if len(body) != 3 {
			t.Fatalf("width %d: want one row per visible provider, got %d:\n%s", w, len(body), plain(body))
		}
		text := plain(body)
		for _, want := range []string{"Claude", "27%", "OpenRouter", "17.98"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d: missing %q in:\n%s", w, want, text)
			}
		}
	}
}

func TestWideSummaryRowShowsBarsResetAndAge(t *testing.T) {
	row := plain(newTab("").Body(frame.Env{Width: 140, Rows: 40}))
	for _, want := range []string{"▕", "↻2h0m", "ago", "1 reset"} {
		if !strings.Contains(row, want) {
			t.Fatalf("missing %q in:\n%s", want, row)
		}
	}
}

func TestUnknownIsAQuestionMarkNotZeroInTheDetail(t *testing.T) {
	for _, row := range strings.Split(plain(newTab("claude").Body(frame.Env{Width: 80, Rows: 40})), "\n") {
		if strings.Contains(row, "unknown window") && (!strings.Contains(row, "?") || strings.Contains(row, "0%")) {
			t.Fatalf("unknown window row = %q", row)
		}
	}
}

func TestSignedOutIsHiddenFromTheListButOpensWhenAsked(t *testing.T) {
	if all := plain(newTab("").Body(frame.Env{Width: 80, Rows: 40})); strings.Contains(all, "xAI") {
		t.Fatalf("signed-out provider listed:\n%s", all)
	}
	tb := newTab("xai")
	if body := plain(tb.Body(frame.Env{Width: 80, Rows: 40})); !strings.Contains(body, "/login xai") {
		t.Fatalf("detail of a signed-out provider must name the login:\n%s", body)
	}
	if crumbs := tb.Crumbs(); len(crumbs) != 1 || crumbs[0] != "xAI" {
		t.Fatalf("crumbs %v", crumbs)
	}
}

func TestErrorAndBackoffAreVisibleAndSanitized(t *testing.T) {
	tb := newTab("codex")
	body := plain(tb.Body(frame.Env{Width: 80, Rows: 40}))
	if !strings.Contains(body, "rate limited red") || strings.Contains(body, "\x1b") {
		t.Fatalf("codex detail:\n%s", body)
	}
	list := plain(newTab("").Body(frame.Env{Width: 80, Rows: 40}))
	if !strings.Contains(list, "retry in 1m (HTTP 429)") {
		t.Fatalf("codex row must carry its state:\n%s", list)
	}
	if d := tb.Description(frame.Env{}); !strings.Contains(d, "held for 1m") {
		t.Fatalf("description %q", d)
	}
}

func TestEnterOpensDetailEscGoesBackToTheSameRow(t *testing.T) {
	tb := newTab("")
	env := frame.Env{Width: 80, Rows: 40}
	tb.Body(env)
	tb.Key("\x1b[B")
	tb.Body(env)
	selected := tb.list.Selected()
	tb.Key("\r")
	if crumbs := tb.Crumbs(); len(crumbs) != 1 {
		t.Fatalf("enter must open a detail, crumbs %v", crumbs)
	}
	if !tb.Key("\x1b") {
		t.Fatal("esc in a detail belongs to the tab")
	}
	tb.Body(env)
	if tb.Crumbs() != nil || tb.list.Selected() != selected {
		t.Fatalf("back must return to %q, got %q", selected, tb.list.Selected())
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
	tb = newTab("codex")
	tb.actions = Actions{Reset: func(id string) { reset = append(reset, id) }}
	tb.views = func() []quota.View {
		v := fixture()
		v[3].Source.Resets = nopResets{}
		return v
	}
	tb.Body(env)
	tb.Key("x")
	if len(refreshed) != 2 || refreshed[0] != "claude" || refreshed[1] != "" {
		t.Fatalf("refreshed = %q", refreshed)
	}
	if placement != 1 || len(reset) != 1 || reset[0] != "codex" {
		t.Fatalf("placement=%d reset=%v", placement, reset)
	}
}

func TestListLettersAreActionsSoTheTabNeverFilters(t *testing.T) {
	if newTab("").Filterable() {
		t.Fatal("bare letters are actions on this tab; typing must not filter")
	}
}

func TestNoticeShowsUntilTheNextKey(t *testing.T) {
	tb := newTab("")
	env := frame.Env{Width: 80, Rows: 40}
	tb.Body(env)
	tb.SetNotice("saved", false)
	if body := plain(tb.Body(env)); !strings.Contains(body, "saved") {
		t.Fatal("notice not shown")
	}
	tb.Key("\x1b[B")
	if body := plain(tb.Body(env)); strings.Contains(body, "saved") {
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

func TestDetailScrollsWhenTallerThanTheBody(t *testing.T) {
	tb := newTab("claude")
	env := frame.Env{Width: 80, Rows: 3}
	first := plain(tb.Body(env))
	tb.Key("\x1b[B")
	tb.Key("\x1b[B")
	if second := plain(tb.Body(env)); second == first {
		t.Fatal("detail did not scroll")
	}
	if above, below := tb.Overflow(); above == 0 || below < 0 {
		t.Fatalf("overflow %d/%d", above, below)
	}
}

func TestUsedAndRemainingFollowTheSetting(t *testing.T) {
	tb := New(fixture, func() Display { return Display{PercentUsed: false, HideSignedOut: true} }, Actions{}, "claude")
	tb.now = func() time.Time { return now }
	body := plain(tb.Body(frame.Env{Width: 80, Rows: 40}))
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

func TestWideRowsFallBackTogetherAndNothingIsCutWhileALowerLevelFits(t *testing.T) {
	two := func(w int) []string {
		return summaryRows(plainTheme{}, fixture()[:1], Display{PercentUsed: true}, 0, w, now)
	}
	full, narrow := two(140)[0], two(40)[0]
	if !strings.Contains(full, "↻") || !strings.Contains(full, "ago") {
		t.Fatalf("wide row lost detail: %q", full)
	}
	if strings.Contains(narrow, "↻") || strings.Contains(narrow, "…") || !strings.Contains(narrow, "27%") {
		t.Fatalf("narrow row must drop detail, not cut: %q", narrow)
	}
}

func TestCreditSourcesLeadWithTheBalanceNotTheUsageWindow(t *testing.T) {
	v := fixture()[1]
	v.Source.Credits = true
	v.Snapshot.Windows = []quota.Window{{ID: "cred", Short: "cred", Used: quota.Frac(1)}}
	row, _ := summaryRow(plainTheme{}, v, Display{PercentUsed: true, CreditHealthyUSD: 50, CreditCriticalUSD: 5}, 10, false, 80, now, levelFull, true)
	if !strings.Contains(row, "17.98") || strings.Contains(row, "100%") {
		t.Fatalf("row %q", row)
	}
}
