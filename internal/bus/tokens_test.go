package bus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

type fakeTracker struct {
	mu        sync.Mutex
	latest    *tokens.Report
	refreshed *tokens.Report
	parts     []tokens.Part
	listeners []func(*tokens.Report, tokens.Reason)
	refreshes int
	breaks    int
}

func (f *fakeTracker) Latest() *tokens.Report { f.mu.Lock(); defer f.mu.Unlock(); return f.latest }
func (f *fakeTracker) OnChange(fn func(*tokens.Report, tokens.Reason)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listeners = append(f.listeners, fn)
	return func() {}
}
func (f *fakeTracker) Refresh(context.Context, tokens.Reason) (tokens.Report, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if f.refreshed == nil {
		return tokens.Report{}, false
	}
	return *f.refreshed, true
}
func (f *fakeTracker) Breakdown(context.Context) ([]tokens.Part, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.breaks++
	return f.parts, f.parts != nil
}
func (f *fakeTracker) emit(r *tokens.Report, reason tokens.Reason) {
	f.mu.Lock()
	fns := append([]func(*tokens.Report, tokens.Reason){}, f.listeners...)
	f.mu.Unlock()
	for _, fn := range fns {
		fn(r, reason)
	}
}

func startTokens(t *testing.T, tr *fakeTracker) *fakeTransport {
	t.Helper()
	tp := newTransport()
	g := newGroup(t)
	if _, err := StartTokens(TokensOptions{Tracker: tr, Transport: tp, Go: g.Go}); err != nil {
		t.Fatal(err)
	}
	return tp
}

func sampleReport(total int) *tokens.Report {
	pct := 12.5
	anchor := tokens.Count{Tokens: total - 10, Source: tokens.SourceProvider}
	return &tokens.Report{
		Provider: "synthetic", Model: "hf:x/y", ContextWindow: 1000, Percent: &pct,
		Tokens: tokens.Count{Tokens: total, Source: tokens.SourceEstimate}, Anchor: &anchor,
		Tail: tokens.Count{Tokens: 10, Source: tokens.SourceEstimate}, TailMessages: 2,
		ComputedAt: time.UnixMilli(1_800_000_000_000),
	}
}

func TestTokensReadyAdvertisesCapabilities(t *testing.T) {
	tp := startTokens(t, &fakeTracker{})
	ready := tp.await(t, TokenPrefix+"ready", 1)[0].payload
	if caps := ready["capabilities"].([]any); len(caps) != 2 || caps[0] != "query" || caps[1] != "context" || ready["v"] != float64(1) {
		t.Fatalf("ready = %v", ready)
	}
}

func TestTokensQueryWithNullOptionsReturnsLatestWithoutRecount(t *testing.T) {
	f := &fakeTracker{latest: sampleReport(100)}
	tp := startTokens(t, f)
	tp.deliver(TokenPrefix+"query", envelope("a", map[string]any{"refresh": nil, "breakdown": nil}))
	tp.deliver(TokenPrefix+"query", map[string]any{"nonce": "b"})
	replies := tp.await(t, TokenPrefix+"query:reply", 2)
	for _, r := range replies {
		p := r.payload["payload"].(map[string]any)
		if p["ok"] != true || p["report"].(map[string]any)["tokens"].(map[string]any)["tokens"] != float64(100) {
			t.Errorf("reply = %v", r.payload)
		}
		if _, has := p["breakdown"]; has {
			t.Errorf("breakdown must be absent unless requested")
		}
	}
	if f.refreshes != 0 || f.breaks != 0 {
		t.Errorf("refreshes=%d breakdowns=%d", f.refreshes, f.breaks)
	}
}

func TestTokensReportMapsToTheV1Shape(t *testing.T) {
	f := &fakeTracker{latest: sampleReport(100)}
	tp := startTokens(t, f)
	tp.deliver(TokenPrefix+"query", envelope("a", map[string]any{}))
	report := tp.await(t, TokenPrefix+"query:reply", 1)[0].payload["payload"].(map[string]any)["report"].(map[string]any)
	if report["computedAt"] != float64(1_800_000_000_000) || report["percent"] != 12.5 || report["contextWindow"] != float64(1000) ||
		report["anchor"].(map[string]any)["source"] != "provider" || report["tailMessages"] != float64(2) {
		t.Fatalf("report = %v", report)
	}
	if _, has := report["pi"]; has {
		t.Errorf("an unknown host count must be absent, report = %v", report)
	}
}

func TestTokensRefreshAndBreakdownAreHonouredAndNeedsAuthIsCoded(t *testing.T) {
	fresh := sampleReport(200)
	fresh.Tokens.Diagnostics = []tokens.Diagnostic{{Provider: "claude", Message: "run /login claude"}}
	f := &fakeTracker{latest: sampleReport(100), refreshed: fresh, parts: []tokens.Part{{Kind: tokens.PartUser, Count: tokens.Count{Tokens: 5, Source: tokens.SourceEstimate}, Items: 1}}}
	tp := startTokens(t, f)
	tp.deliver(TokenPrefix+"query", envelope("a", map[string]any{"refresh": true, "breakdown": true}))
	p := tp.await(t, TokenPrefix+"query:reply", 1)[0].payload["payload"].(map[string]any)
	count := p["report"].(map[string]any)["tokens"].(map[string]any)
	diag := count["diagnostics"].([]any)[0].(map[string]any)
	if count["tokens"] != float64(200) || diag["code"] != "needs-auth" || diag["provider"] != "claude" {
		t.Fatalf("report tokens = %v", count)
	}
	part := p["breakdown"].([]any)[0].(map[string]any)
	if part["kind"] != "user" || part["items"] != float64(1) {
		t.Errorf("breakdown = %v", p["breakdown"])
	}
}

func TestTokensUnavailableBreakdownIsOmittedAndNoReportIsNull(t *testing.T) {
	tp := startTokens(t, &fakeTracker{})
	tp.deliver(TokenPrefix+"query", envelope("a", map[string]any{"breakdown": true}))
	p := tp.await(t, TokenPrefix+"query:reply", 1)[0].payload["payload"].(map[string]any)
	if rep, has := p["report"]; !has || rep != nil {
		t.Errorf("report must be present and null, payload = %v", p)
	}
	if _, has := p["breakdown"]; has {
		t.Errorf("breakdown = %v", p["breakdown"])
	}
}

func TestTokensMalformedOptionsGetFailure(t *testing.T) {
	tp := startTokens(t, &fakeTracker{})
	tp.deliver(TokenPrefix+"query", envelope("a", map[string]any{"refresh": "yes"}))
	p := tp.await(t, TokenPrefix+"query:reply", 1)[0].payload["payload"].(map[string]any)
	if p["ok"] != false || p["error"] == "" {
		t.Fatalf("payload = %v", p)
	}
}

func TestTokensContextBroadcastsEveryChangeIncludingClearsAndRepeats(t *testing.T) {
	f := &fakeTracker{}
	tp := startTokens(t, f)
	r := sampleReport(100)
	f.emit(r, tokens.ReasonMessageEnd)
	f.emit(r, tokens.ReasonMessageEnd) // identical values are still two events
	f.emit(nil, tokens.ReasonModelSelect)
	got := tp.await(t, TokenPrefix+"context", 3)
	if got[0].payload["reason"] != "message_end" || got[1].payload["report"] == nil {
		t.Errorf("repeats collapsed: %v", got)
	}
	if rep, has := got[2].payload["report"]; !has || rep != nil || got[2].payload["reason"] != "model_select" {
		t.Errorf("clear = %v", got[2].payload)
	}
}

func TestTokensClosedServiceSendsNothing(t *testing.T) {
	f := &fakeTracker{}
	tp := newTransport()
	g := newGroup(t)
	if _, err := StartTokens(TokensOptions{Tracker: f, Transport: tp, Go: g.Go}); err != nil {
		t.Fatal(err)
	}
	tp.await(t, TokenPrefix+"ready", 1)
	g.Close()
	f.emit(sampleReport(1), tokens.ReasonRequest)
	tp.deliver(TokenPrefix+"query", envelope("late", map[string]any{}))
	tp.settle()
	if len(tp.on(TokenPrefix+"context")) != 0 || len(tp.on(TokenPrefix+"query:reply")) != 0 {
		t.Error("a closed service must not reply or broadcast")
	}
}
