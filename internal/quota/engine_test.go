package quota

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

type fakeResets struct{ consumed atomic.Int32 }

func (f *fakeResets) List(context.Context, Env) (ResetList, error) {
	return ResetList{Available: 1}, nil
}
func (f *fakeResets) Consume(context.Context, Env, string) (ResetOutcome, error) {
	f.consumed.Add(1)
	return ResetOutcome{Code: ResetDone, WindowsReset: 2}, nil
}

type harness struct {
	e     *Engine
	calls atomic.Int32
	fetch func() (Snapshot, error)
	now   atomic.Int64
	cfg   Config
	auth  *Auth
}

func newHarness(t *testing.T, path string) *harness {
	h := &harness{cfg: Config{Interval: time.Minute}, auth: &Auth{Token: "tok"}}
	h.now.Store(1_000_000)
	h.fetch = func() (Snapshot, error) {
		return Snapshot{Provider: "claude", FetchedAt: h.now.Load(), Source: FromEndpoint, Windows: []Window{{ID: "5h", Used: Frac(0.1)}}}, nil
	}
	src := &Source{ID: "claude", Label: "Claude", Aliases: []string{"cl"}, ParserVersion: 1, Resets: &fakeResets{},
		Fetch: func(ctx context.Context, env Env) (Snapshot, error) {
			h.calls.Add(1)
			a, err := env.Auth(ctx)
			if err != nil {
				return Snapshot{}, err
			}
			if a == nil {
				return Snapshot{Provider: "claude", FetchedAt: h.now.Load(), Source: FromEndpoint, NeedsAuth: true}, nil
			}
			return h.fetch()
		},
		Headers: func(m map[string]string, now time.Time) *Snapshot {
			if m["x-claude"] == "" {
				return nil
			}
			return &Snapshot{Windows: []Window{{ID: "5h", Used: Frac(0.9)}}}
		}}
	other := &Source{ID: "xai", Label: "xAI", Fetch: func(context.Context, Env) (Snapshot, error) { return Snapshot{}, errors.New("unused") }}
	h.e = NewEngine(Options{
		Registry: NewRegistry(src, other), CachePath: path,
		Config:  func() Config { return h.cfg },
		Now:     func() time.Time { return time.UnixMilli(h.now.Load()) },
		AuthFor: func(context.Context, string) (*Auth, error) { return h.auth, nil },
	})
	return h
}

func TestRefreshStoresSnapshotAndSharesItAcrossEngines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	a, b := newHarness(t, path), newHarness(t, path)
	a.cfg.Providers, b.cfg.Providers = []string{"cl"}, []string{"cl"}
	ctx := context.Background()
	if err := a.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.e.Stop()
	a.e.Stop() // wait for the initial background refresh
	if err := a.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.e.Stop()
	_ = b.e.Refresh(ctx, "claude", RefreshOptions{})
	if got := a.calls.Load() + b.calls.Load(); got != 1 {
		t.Fatalf("fresh shared entry must be fetched once, got %d fetches", got)
	}
	v := b.e.View()
	if len(v) != 1 || v[0].Snapshot == nil || *v[0].Snapshot.Windows[0].Used != 0.1 {
		t.Fatalf("view = %+v", v)
	}
}

func TestMissingCredentialIsNeedsAuthWithoutBackoff(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	h.auth = nil
	if err := h.e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer h.e.Stop()
	_ = h.e.Refresh(context.Background(), "claude", RefreshOptions{Force: true})
	v := h.e.View()[0]
	if v.Snapshot == nil || !v.Snapshot.NeedsAuth || v.Backoff != nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestRateLimitHoldsEvenForcedRefresh(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	h.fetch = func() (Snapshot, error) {
		return Snapshot{}, &httpx.StatusError{Provider: "claude", Status: http.StatusTooManyRequests, RetryAfter: 5 * time.Minute, HasRetryAfter: true}
	}
	ctx := context.Background()
	_ = h.e.Start(ctx)
	defer h.e.Stop()
	_ = h.e.Refresh(ctx, "claude", RefreshOptions{Force: true})
	n := h.calls.Load()
	v := h.e.View()[0]
	if v.Backoff == nil || v.Backoff.Status != 429 || v.Backoff.Until-h.now.Load() != int64(5*time.Minute/time.Millisecond) {
		t.Fatalf("backoff = %+v", v.Backoff)
	}
	_ = h.e.Refresh(ctx, "claude", RefreshOptions{Force: true})
	if h.calls.Load() != n {
		t.Fatal("a forced refresh bypassed an active 429 hold")
	}
}

func TestHeadersOverlayWithoutMovingEndpointFreshness(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	ctx := context.Background()
	_ = h.e.Start(ctx)
	defer h.e.Stop()
	_ = h.e.Refresh(ctx, "claude", RefreshOptions{Force: true})
	h.now.Add(10_000)
	if id := h.e.IngestHeaders(ctx, map[string]string{"X-Claude": "1"}, "", false); id != "claude" {
		t.Fatalf("ingest = %q", id)
	}
	v := h.e.View()[0]
	if *v.Snapshot.Windows[0].Used != 0.9 || v.FetchedAt != 1_000_000 {
		t.Fatalf("view = %+v fetchedAt=%d", v.Snapshot, v.FetchedAt)
	}
	if id := h.e.IngestHeaders(ctx, map[string]string{"x-claude": "1"}, "xai", true); id != "" {
		t.Fatalf("strict hint for another provider must not claim: %q", id)
	}
	if id := h.e.IngestHeaders(ctx, map[string]string{"x-other": "1"}, "", false); id != "" {
		t.Fatalf("unrecognized headers claimed by %q", id)
	}
}

func TestConsumeResetAlwaysRefreshesAndNotifiesOnlyOnSpend(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	ctx := context.Background()
	_ = h.e.Start(ctx)
	defer h.e.Stop()
	var spent atomic.Int32
	h.e.OnResetConsumed(func(ResetOutcome, Requester) { spent.Add(1) })
	before := h.calls.Load()
	out, err := h.e.ConsumeReset(ctx, "claude", "c1", FromCommand)
	if err != nil || out.Code != ResetDone || spent.Load() != 1 {
		t.Fatalf("outcome=%+v err=%v spent=%d", out, err, spent.Load())
	}
	if h.calls.Load() <= before {
		t.Fatal("consume must be followed by a forced refresh")
	}
	if _, err := h.e.ConsumeReset(ctx, "xai", "c", FromBus); err == nil {
		t.Fatal("provider without reset support must be refused")
	}
	if _, err := h.e.ConsumeReset(ctx, "nope", "c", FromBus); err == nil {
		t.Fatal("unknown provider must be refused")
	}
}

func TestProviderSelection(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	ids := func() []string {
		var o []string
		for _, s := range h.e.Sources() {
			o = append(o, s.ID)
		}
		return o
	}
	if got := ids(); len(got) != 2 {
		t.Fatalf("empty list enables all: %v", got)
	}
	h.cfg.Providers = []string{"CL", "bogus"}
	if got := ids(); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("alias selection: %v", got)
	}
	h.cfg.Providers = []string{"bogus"}
	if got := ids(); len(got) != 0 {
		t.Fatalf("only unknown names enable none: %v", got)
	}
	if h.e.Registry().Resolve("claude") == nil || h.e.Registry().Resolve("XAI") == nil || h.e.Registry().Resolve("Claude") == nil {
		t.Fatal("id/label resolution")
	}
}

func TestStopCancelsWorkAndRefusesLateRefresh(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "q.json"))
	started, release := make(chan struct{}), make(chan struct{})
	h.fetch = func() (Snapshot, error) { close(started); <-release; return Snapshot{FetchedAt: 1}, nil }
	ctx := context.Background()
	_ = h.e.Start(ctx)
	<-started
	done := make(chan struct{})
	go func() { h.e.Stop(); close(done) }()
	close(release)
	<-done
	if err := h.e.Refresh(ctx, "claude", RefreshOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	n := h.calls.Load()
	_ = h.e.Refresh(ctx, "claude", RefreshOptions{Force: true})
	if h.calls.Load() != n {
		t.Fatal("refresh ran after stop")
	}
}
