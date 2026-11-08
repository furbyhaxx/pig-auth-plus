package resetflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

type fakeEngine struct {
	list     quota.ResetList
	outcome  quota.ResetOutcome
	consumeE error
	consumed int
	srcs     []*quota.Source
}

func (f *fakeEngine) Sources() []*quota.Source  { return f.srcs }
func (f *fakeEngine) Registry() *quota.Registry { return quota.NewRegistry(f.srcs...) }
func (f *fakeEngine) ResetSupport(string) bool  { return true }
func (f *fakeEngine) ListResets(context.Context, string) (quota.ResetList, error) {
	return f.list, nil
}
func (f *fakeEngine) ConsumeReset(context.Context, string, string, quota.Requester) (quota.ResetOutcome, error) {
	f.consumed++
	return f.outcome, f.consumeE
}
func (f *fakeEngine) View() []quota.View { return nil }

type fakeDialogs struct {
	interactive bool
	choose      string
	answered    bool
	title       string
}

func (d *fakeDialogs) Interactive() bool { return d.interactive }
func (d *fakeDialogs) Select(title string, _ []string) (string, bool, error) {
	d.title = title
	return d.choose, d.answered, nil
}

func ms(v int64) *int64 { return &v }

func setup() *fakeEngine {
	return &fakeEngine{
		srcs: []*quota.Source{{ID: "claude", Label: "Claude"}, {ID: "codex", Label: "ChatGPT", Resets: struct{ quota.Resets }{}}},
		list: quota.ResetList{Provider: "codex", Available: 2, Credits: []quota.ResetCredit{
			{ID: "late", Status: "available", Type: "t", ExpiresAtMs: ms(2000)},
			{ID: "soon", Status: "available", Type: "t", ExpiresAtMs: ms(1000)},
			{ID: "used", Status: "redeemed", Type: "t"},
			{ID: "unsupported", Status: "available", Type: "t", ExpiresAtMs: ms(10), SupportedByPlan: new(bool)},
		}},
		outcome: quota.ResetOutcome{Provider: "codex", Code: quota.ResetDone, WindowsReset: 2},
	}
}

func TestPickSpendsSoonestExpiringRedeemableCredit(t *testing.T) {
	c, ok := Pick(setup().list, "")
	if !ok || c.ID != "soon" {
		t.Fatalf("picked %+v ok=%v", c, ok)
	}
	if c, ok := Pick(setup().list, "late"); !ok || c.ID != "late" {
		t.Fatalf("explicit pick %+v", c)
	}
	if _, ok := Pick(setup().list, "used"); ok {
		t.Fatal("a redeemed credit must not be pickable")
	}
}

func TestDefaultProviderIsFirstResetCapable(t *testing.T) {
	if id, ok := DefaultProvider(setup()); !ok || id != "codex" {
		t.Fatalf("default = %q %v", id, ok)
	}
}

func TestNothingIsSpentWithoutTheExplicitSecondChoice(t *testing.T) {
	for name, d := range map[string]*fakeDialogs{
		"keep":      {interactive: true, choose: Keep, answered: true},
		"dismissed": {interactive: true, answered: false},
		"headless":  {interactive: false},
	} {
		e := setup()
		r, err := Run(context.Background(), e, d, "", quota.FromCommand)
		if err != nil || e.consumed != 0 || strings.Contains(r.Text, "credit spent") {
			t.Fatalf("%s: consumed=%d text=%q err=%v", name, e.consumed, r.Text, err)
		}
	}
}

func TestExplicitSpendConsumesExactlyOnceAndReports(t *testing.T) {
	e := setup()
	r, err := Run(context.Background(), e, &fakeDialogs{interactive: true, choose: Spend, answered: true}, "ChatGPT", quota.FromCommand)
	if err != nil || e.consumed != 1 || r.Warn || !strings.Contains(r.Text, "2 windows reset, one credit spent") {
		t.Fatalf("consumed=%d result=%+v err=%v", e.consumed, r, err)
	}
	e = setup()
	e.outcome.Code = quota.NothingToReset
	r, _ = Run(context.Background(), e, &fakeDialogs{interactive: true, choose: Spend, answered: true}, "", quota.FromCommand)
	if !r.Warn || strings.Contains(r.Text, "credit spent") {
		t.Fatalf("nothing_to_reset must not claim a spend: %+v", r)
	}
}

func TestFailedConsumeIsReportedAndNotRetried(t *testing.T) {
	e := setup()
	e.consumeE = errors.New("codex: POST returned HTTP 502; the credit may already be spent")
	_, err := Run(context.Background(), e, &fakeDialogs{interactive: true, choose: Spend, answered: true}, "", quota.FromCommand)
	if err == nil || e.consumed != 1 {
		t.Fatalf("consumed=%d err=%v", e.consumed, err)
	}
}

func TestNoRedeemableCreditAsksNothing(t *testing.T) {
	e := setup()
	e.list.Credits = []quota.ResetCredit{{ID: "used", Status: "redeemed"}}
	d := &fakeDialogs{interactive: true, choose: Spend, answered: true}
	r, err := Run(context.Background(), e, d, "", quota.FromCommand)
	if err != nil || e.consumed != 0 || d.title != "" || !r.Warn {
		t.Fatalf("consumed=%d title=%q result=%+v err=%v", e.consumed, d.title, r, err)
	}
}

func TestUnknownProviderIsRejected(t *testing.T) {
	if _, err := Run(context.Background(), setup(), &fakeDialogs{interactive: true}, "nope", quota.FromCommand); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestPlainStripsTerminalSequences(t *testing.T) {
	if got := Plain("a\x1b[31mred\x1b[0m\x1b]0;title\x07b\x00c"); got != "aredbc" {
		t.Fatalf("Plain = %q", got)
	}
}

func TestDateIsZurich24h(t *testing.T) {
	v := int64(1793008800000) // 2026-10-26T10:00:00Z, CET
	if got := Date(&v); got != "26.10.2026 11:00" {
		t.Fatalf("Date = %q", got)
	}
}
