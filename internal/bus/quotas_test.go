package bus

import (
	"context"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

var (
	now     = time.UnixMilli(1_800_000_000_000)
	claude  = &quota.Source{ID: "claude", Label: "Claude", Short: "cl", Aliases: []string{"cl", "cc"}, MinPoll: 5 * time.Minute}
	codex   = &quota.Source{ID: "codex", Label: "Codex", Short: "cox", Aliases: []string{"openai"}, Resets: stubResets{}}
	fetched = now.Add(-10 * time.Second).UnixMilli()
)

type stubResets struct{}

func (stubResets) List(context.Context, quota.Env) (quota.ResetList, error) {
	return quota.ResetList{}, nil
}
func (stubResets) Consume(context.Context, quota.Env, string) (quota.ResetOutcome, error) {
	return quota.ResetOutcome{}, nil
}

func startQuotas(t *testing.T, e *fakeEngine, approve Approver) (*fakeTransport, *Quotas) {
	t.Helper()
	tr := newTransport()
	g := newGroup(t)
	q, err := StartQuotas(QuotasOptions{
		Engine: e, Transport: tr, Go: g.Go, Approve: approve,
		Interval: func() time.Duration { return time.Minute }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr, q
}

func usedWindow(f float64) quota.Window {
	return quota.Window{ID: "5h", Short: "5h", Label: "Session", Used: &f, ResetAtMs: quota.Ms(now.Add(time.Hour).UnixMilli())}
}

func okView(s *quota.Source, w ...quota.Window) quota.View {
	return quota.View{Source: s, FetchedAt: fetched, Snapshot: &quota.Snapshot{Provider: s.ID, FetchedAt: fetched, Source: quota.FromEndpoint, Windows: w}}
}

func envelope(nonce string, payload any) map[string]any {
	return map[string]any{"nonce": nonce, "payload": payload}
}

func TestReadyListsEnabledProvidersWithAliases(t *testing.T) {
	tr, _ := startQuotas(t, newEngine(claude, codex), nil)
	ready := tr.await(t, QuotaPrefix+"ready", 1)[0].payload
	providers := ready["providers"].([]any)
	if len(providers) != 2 || providers[0].(map[string]any)["id"] != "claude" {
		t.Fatalf("providers = %v", providers)
	}
	if aliases := providers[0].(map[string]any)["aliases"].([]any); len(aliases) != 2 {
		t.Fatalf("aliases = %v", aliases)
	}
}

func TestQueryAcceptsNullOptionsAndReportsUnknownAsAbsent(t *testing.T) {
	e := newEngine(claude)
	e.views["claude"] = okView(claude, quota.Window{ID: "7d", Short: "7d", Label: "Weekly"})
	tr, _ := startQuotas(t, e, nil)
	tr.deliver(QuotaPrefix+"query", envelope("n1", map[string]any{"providers": nil, "maxAgeSec": nil}))
	reply := tr.await(t, QuotaPrefix+"query:reply", 1)[0].payload
	if reply["nonce"] != "n1" {
		t.Fatalf("nonce = %v", reply["nonce"])
	}
	payload := reply["payload"].(map[string]any)
	if payload["ok"] != true || payload["v"] != float64(1) {
		t.Fatalf("payload = %v", payload)
	}
	row := payload["report"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	win := row["windows"].([]any)[0].(map[string]any)
	for _, k := range []string{"usedFraction", "usedPercent", "remainingPercent", "resetAt", "resetInSec"} {
		if _, present := win[k]; present {
			t.Errorf("unknown %s must be absent, window = %v", k, win)
		}
	}
	if row["state"] != StateOK || row["ageSec"] != float64(10) {
		t.Errorf("row = %v", row)
	}
	if len(e.refreshes) != 0 {
		t.Errorf("a query without maxAgeSec must not refresh")
	}
}

func TestQueryRefreshesOldDataWithMinimumAgeOfOneSecond(t *testing.T) {
	e := newEngine(claude)
	tr, _ := startQuotas(t, e, nil)
	tr.deliver(QuotaPrefix+"query", envelope("n", map[string]any{"maxAgeSec": 0}))
	tr.await(t, QuotaPrefix+"query:reply", 1)
	if len(e.refreshes) != 1 || e.refreshes[0].MinAge != time.Second || e.refreshes[0].Force {
		t.Fatalf("refreshes = %+v", e.refreshes)
	}
}

func TestExplicitEmptyProvidersSelectsNoneAndUnknownOrDisabledFail(t *testing.T) {
	e := newEngine(claude, codex)
	e.enabled = []string{"claude"}
	tr, _ := startQuotas(t, e, nil)
	tr.deliver(QuotaPrefix+"query", envelope("empty", map[string]any{"providers": []any{}}))
	tr.deliver(QuotaPrefix+"query", envelope("unknown", map[string]any{"providers": []any{"nope"}}))
	tr.deliver(QuotaPrefix+"query", envelope("disabled", map[string]any{"providers": []any{"openai"}}))
	tr.deliver(QuotaPrefix+"query", envelope("alias", map[string]any{"providers": []any{"CC", "claude"}}))
	got := map[string]map[string]any{}
	for _, m := range tr.await(t, QuotaPrefix+"query:reply", 4) {
		got[m.payload["nonce"].(string)] = m.payload["payload"].(map[string]any)
	}
	if n := len(got["empty"]["report"].(map[string]any)["providers"].([]any)); n != 0 {
		t.Errorf("empty list selected %d providers", n)
	}
	for _, n := range []string{"unknown", "disabled"} {
		if got[n]["ok"] != false || got[n]["error"] == "" {
			t.Errorf("%s = %v", n, got[n])
		}
	}
	if n := len(got["alias"]["report"].(map[string]any)["providers"].([]any)); n != 1 {
		t.Errorf("alias and id deduplicate to one provider, got %d", n)
	}
}

func TestMalformedEnvelopeIsDroppedAndMalformedPayloadGetsFailure(t *testing.T) {
	tr, _ := startQuotas(t, newEngine(claude), nil)
	tr.deliver(QuotaPrefix+"query", map[string]any{"payload": map[string]any{}})
	tr.deliver(QuotaPrefix+"query", "not an object")
	tr.deliver(QuotaPrefix+"query", envelope("bad", map[string]any{"maxAgeSec": -1}))
	got := tr.await(t, QuotaPrefix+"query:reply", 1)
	tr.settle()
	if n := len(tr.on(QuotaPrefix + "query:reply")); n != 1 || got[0].payload["nonce"] != "bad" {
		t.Fatalf("replies = %v", tr.on(QuotaPrefix+"query:reply"))
	}
	if got[0].payload["payload"].(map[string]any)["ok"] != false {
		t.Errorf("payload = %v", got[0].payload)
	}
}

func TestSnapshotOnlyForChangedProvidersAndNotForClockDrift(t *testing.T) {
	e := newEngine(claude, codex)
	e.views["claude"] = okView(claude, usedWindow(0.1))
	tr, _ := startQuotas(t, e, nil)
	e.set(okView(claude, usedWindow(0.1))) // identical
	tr.settle()
	if n := len(tr.on(QuotaPrefix + "snapshot")); n != 0 {
		t.Fatalf("unchanged views broadcast %d snapshots", n)
	}
	e.set(okView(claude, usedWindow(0.2)))
	got := tr.await(t, QuotaPrefix+"snapshot", 1)
	tr.settle()
	if n := len(tr.on(QuotaPrefix + "snapshot")); n != 1 {
		t.Fatalf("snapshots = %d", n)
	}
	provider := got[0].payload["provider"].(map[string]any)
	if provider["id"] != "claude" || provider["windows"].([]any)[0].(map[string]any)["usedPercent"] != float64(20) {
		t.Errorf("provider = %v", provider)
	}
}

func TestAuthAndRateLimitEventsFireOnTransitionsOnly(t *testing.T) {
	e := newEngine(claude)
	tr, _ := startQuotas(t, e, nil)
	signedOut := quota.View{Source: claude, Snapshot: &quota.Snapshot{Provider: "claude", NeedsAuth: true, Error: "run /login claude"}}
	e.set(signedOut)
	e.set(signedOut)
	held := quota.View{Source: claude, Backoff: &quota.Backoff{Until: now.Add(time.Minute).UnixMilli(), Status: 429, Error: "slow down"}}
	e.set(held)
	e.set(held)
	events := tr.await(t, QuotaPrefix+"event", 2)
	tr.settle()
	events = tr.on(QuotaPrefix + "event")
	if len(events) != 2 || events[0].payload["kind"] != "auth-required" || events[0].payload["message"] != "run /login claude" ||
		events[1].payload["kind"] != "rate-limited" || events[1].payload["status"] != float64(429) {
		t.Fatalf("events = %v", events)
	}
}

func TestStateFollowsStalenessAndFloors(t *testing.T) {
	old := okView(claude)
	old.FetchedAt = now.Add(-11 * time.Minute).UnixMilli() // floor 5 min: stale after 10
	if row := BuildProvider(old, now, time.Minute); row.State != StateStale {
		t.Errorf("state = %s", row.State)
	}
	old.FetchedAt = now.Add(-9 * time.Minute).UnixMilli()
	if row := BuildProvider(old, now, time.Minute); row.State != StateOK {
		t.Errorf("state = %s", row.State)
	}
	if row := BuildProvider(quota.View{Source: codex}, now, time.Minute); row.State != StateUnknown {
		t.Errorf("state = %s", row.State)
	}
}

func resetEngine() *fakeEngine {
	e := newEngine(claude, codex)
	e.list = quota.ResetList{Provider: "codex", Available: 1, Credits: []quota.ResetCredit{{ID: "c1", Status: "available"}}}
	e.outcome = quota.ResetOutcome{Provider: "codex", Code: quota.ResetDone, CreditID: "c1", WindowsReset: 2}
	return e
}

func TestResetNeedsConfirmAndApprovalAndNeverSpendsOtherwise(t *testing.T) {
	e := resetEngine()
	asked := 0
	tr, _ := startQuotas(t, e, func(context.Context, Approval) (bool, error) { asked++; return false, nil })
	tr.deliver(QuotaPrefix+"reset", envelope("noconfirm", map[string]any{"provider": "codex"}))
	tr.deliver(QuotaPrefix+"reset", envelope("falseconfirm", map[string]any{"provider": "codex", "confirm": false}))
	tr.deliver(QuotaPrefix+"reset", envelope("declined", map[string]any{"provider": "codex", "confirm": true}))
	replies := tr.await(t, QuotaPrefix+"reset:reply", 3)
	for _, r := range replies {
		if r.payload["payload"].(map[string]any)["ok"] != false {
			t.Errorf("%v must be refused", r.payload)
		}
	}
	if e.consumeCount() != 0 {
		t.Fatal("a refused reset spent a credit")
	}
	if asked != 1 {
		t.Errorf("dialog asked %d times; only the confirmed request may ask", asked)
	}
}

func TestApprovedResetConsumesOnceAndBroadcastsTheRequester(t *testing.T) {
	e := resetEngine()
	tr, _ := startQuotas(t, e, func(_ context.Context, a Approval) (bool, error) {
		if a.Credit.ID != "c1" || a.Provider != "codex" {
			t.Errorf("approval = %+v", a)
		}
		return true, nil
	})
	tr.deliver(QuotaPrefix+"reset", envelope("go", map[string]any{"provider": "OpenAI", "creditId": nil, "confirm": true}))
	reply := tr.await(t, QuotaPrefix+"reset:reply", 1)[0].payload["payload"].(map[string]any)
	if reply["ok"] != true || reply["outcome"].(map[string]any)["code"] != "reset" {
		t.Fatalf("reply = %v", reply)
	}
	if e.consumeCount() != 1 || e.consumes[0] != "codex/c1" {
		t.Fatalf("consumes = %v", e.consumes)
	}
	ev := tr.await(t, QuotaPrefix+"event", 1)[0].payload
	if ev["kind"] != "reset-consumed" || ev["requester"] != "bus" || ev["windowsReset"] != float64(2) {
		t.Errorf("event = %v", ev)
	}
}

func TestResetRejectsUnavailableCreditAndUnsupportedProvider(t *testing.T) {
	e := resetEngine()
	tr, _ := startQuotas(t, e, func(context.Context, Approval) (bool, error) { return true, nil })
	tr.deliver(QuotaPrefix+"reset", envelope("other", map[string]any{"provider": "codex", "creditId": "zzz", "confirm": true}))
	reply := tr.await(t, QuotaPrefix+"reset:reply", 1)[0].payload["payload"].(map[string]any)
	if reply["ok"] != false || e.consumeCount() != 0 {
		t.Fatalf("reply = %v consumes = %d", reply, e.consumeCount())
	}
}

func TestStoppedServiceSendsNothing(t *testing.T) {
	e := newEngine(claude)
	tr := newTransport()
	g := newGroup(t)
	if _, err := StartQuotas(QuotasOptions{Engine: e, Transport: tr, Go: g.Go, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	tr.await(t, QuotaPrefix+"ready", 1)
	g.Close()
	before := len(tr.on(QuotaPrefix + "snapshot"))
	e.set(okView(claude, usedWindow(0.5)))
	tr.deliver(QuotaPrefix+"query", envelope("late", map[string]any{}))
	tr.settle()
	if len(tr.on(QuotaPrefix+"snapshot")) != before || len(tr.on(QuotaPrefix+"query:reply")) != 0 {
		t.Error("a closed service must not reply or broadcast")
	}
}
