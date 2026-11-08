package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newTestCache(t *testing.T, path string, clk *clock) *Cache[string] {
	return NewCache(CacheOptions[string]{
		Path: path, Now: clk.now, HoldStatuses: []int{403, 429},
		Version: func(string) int { return 2 },
		Parse: func(raw json.RawMessage) (string, bool) {
			var s string
			return s, json.Unmarshal(raw, &s) == nil && s != ""
		},
	})
}

func TestTwoSessionsShareOneRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	clk := &clock{t: time.UnixMilli(1_000_000)}
	a, b := newTestCache(t, path, clk), newTestCache(t, path, clk)
	ctx := context.Background()
	ok, err := a.TryAcquire(ctx, "claude", AcquireOptions{MinAge: time.Minute})
	if err != nil || !ok {
		t.Fatalf("first acquire = %v %v", ok, err)
	}
	if ok, _ := b.TryAcquire(ctx, "claude", AcquireOptions{MinAge: time.Minute}); ok {
		t.Fatal("second session acquired a leased key")
	}
	if err := a.Put(ctx, "claude", "snap"); err != nil {
		t.Fatal(err)
	}
	if changed, _ := b.Sync(ctx, false); !changed {
		t.Fatal("second session did not see the write")
	}
	if e, _ := b.Get("claude"); e.Value != "snap" {
		t.Fatalf("entry = %+v", e)
	}
	if ok, _ := b.TryAcquire(ctx, "claude", AcquireOptions{MinAge: time.Minute}); ok {
		t.Fatal("fresh entry must not be refreshed")
	}
	clk.add(2 * time.Minute)
	if ok, _ := b.TryAcquire(ctx, "claude", AcquireOptions{MinAge: time.Minute}); !ok {
		t.Fatal("stale entry must be refreshable")
	}
}

func TestForcedRefreshRespectsRateLimitHoldOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	clk := &clock{t: time.UnixMilli(1_000_000)}
	c := newTestCache(t, path, clk)
	ctx := context.Background()
	_ = c.Fail(ctx, "a", time.Minute, 500, "boom")
	_ = c.Fail(ctx, "b", time.Minute, 429, "slow down")
	if ok, _ := c.TryAcquire(ctx, "a", AcquireOptions{}); ok {
		t.Fatal("ordinary backoff must hold an automatic refresh")
	}
	if ok, _ := c.TryAcquire(ctx, "a", AcquireOptions{Force: true}); !ok {
		t.Fatal("forced refresh must bypass ordinary backoff")
	}
	if ok, _ := c.TryAcquire(ctx, "b", AcquireOptions{Force: true}); ok {
		t.Fatal("forced refresh must not bypass a 429 hold")
	}
	clk.add(2 * time.Minute)
	if _, held := c.Backoff("b"); held {
		t.Fatal("backoff must expire")
	}
}

func TestOlderParserVersionAndCorruptFilesAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	clk := &clock{t: time.UnixMilli(1_000_000)}
	old := `{"version":1,"entries":{"x":{"value":"v","fetchedAt":1,"updatedAt":1,"parserVersion":1}}}`
	_ = os.WriteFile(path, []byte(old), 0o600)
	c := newTestCache(t, path, clk)
	if changed, _ := c.Sync(context.Background(), true); changed {
		t.Fatal("entry from an older parser must be ignored")
	}
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if changed, err := c.Sync(context.Background(), true); changed || err != nil {
		t.Fatalf("corrupt file: %v %v", changed, err)
	}
	if err := c.Put(context.Background(), "x", "new"); err != nil {
		t.Fatalf("a corrupt file must be replaced on the next write: %v", err)
	}
}

func TestBackoffPolicy(t *testing.T) {
	p := BackoffPolicy{Default: 30 * time.Second, RateLimit: time.Minute, Max: 15 * time.Minute, RateLimitStatuses: []int{403, 429}}
	cases := []struct {
		status int
		after  time.Duration
		want   time.Duration
	}{
		{500, 0, 30 * time.Second}, {0, 0, 30 * time.Second},
		{429, 0, time.Minute}, {403, 5 * time.Minute, 5 * time.Minute}, {429, time.Hour, 15 * time.Minute},
		{500, time.Hour, 30 * time.Second},
	}
	for _, c := range cases {
		if got := p.For(c.status, c.after); got != c.want {
			t.Errorf("For(%d, %s) = %s, want %s", c.status, c.after, got, c.want)
		}
	}
}
