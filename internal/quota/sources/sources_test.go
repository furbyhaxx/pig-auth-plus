package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	m, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func byID(s quota.Snapshot, id string) *quota.Window {
	for i := range s.Windows {
		if s.Windows[i].ID == id {
			return &s.Windows[i]
		}
	}
	return nil
}

func used(t *testing.T, w *quota.Window) float64 {
	t.Helper()
	if w == nil || w.Used == nil {
		t.Fatalf("window %+v has no usage", w)
	}
	return *w.Used
}

func TestClaudeLimitsKeepServerOrderAndIgnoreLegacy(t *testing.T) {
	s, err := ParseClaude(fixture(t, "claude-usage.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range s.Windows {
		ids = append(ids, w.ID)
	}
	want := []string{"scoped:fable", "5h", "7d"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if used(t, byID(s, "5h")) != 0.2 || byID(s, "7d").ResetAtMs != nil {
		t.Fatalf("windows = %+v", s.Windows)
	}
	if byID(s, "5h").ResetAtMs == nil || *byID(s, "5h").ResetAtMs != time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("reset = %v", byID(s, "5h").ResetAtMs)
	}
}

func TestClaudeLegacyWindowsAndMissingNumberStaysUnknown(t *testing.T) {
	s, err := ParseClaude(map[string]any{
		"five_hour":   map[string]any{"utilization": 0.0, "resets_at": nil},
		"seven_day":   map[string]any{"resets_at": "2026-10-27T00:00:00Z"},
		"extra_usage": map[string]any{"is_enabled": true, "monthly_limit": 5000.0, "used_credits": 1250.0, "currency": "usd"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if used(t, byID(s, "5h")) != 0 {
		t.Fatal("an explicit 0% is real usage")
	}
	if byID(s, "7d").Used != nil {
		t.Fatal("a missing utilization must stay unknown")
	}
	extra := byID(s, "extra_usage")
	if used(t, extra) != 0.25 || extra.Value != "$12.50 / $50.00" {
		t.Fatalf("extra = %+v", extra)
	}
}

func TestClaudeRejectsUnrecognizedPayload(t *testing.T) {
	if _, err := ParseClaude(map[string]any{"hello": "world"}, now); err == nil {
		t.Fatal("expected an error")
	}
}

func TestClaudeHeaders(t *testing.T) {
	s := claudeHeaders(map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.25",
		"anthropic-ratelimit-unified-5h-reset":       "1793000000",
		"anthropic-ratelimit-unified-7d-utilization": "",
		"anthropic-ratelimit-unified-7d-reset":       "1793500000",
	}, now)
	if s == nil || used(t, byID(*s, "5h")) != 0.25 || byID(*s, "7d").Used != nil || byID(*s, "7d").ResetAtMs == nil {
		t.Fatalf("snapshot = %+v", s)
	}
	if claudeHeaders(map[string]string{"x": "y"}, now) != nil {
		t.Fatal("unrelated headers must not be claimed")
	}
}

func TestAntigravityBucketsAndImportantPerGroup(t *testing.T) {
	s, err := ParseAntigravity(fixture(t, "antigravity-quota.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if used(t, byID(s, "gemini-weekly")) != 0 {
		t.Fatalf("remaining 1 means used 0: %+v", byID(s, "gemini-weekly"))
	}
	argon := byID(s, "gemini-argon-wtu-weekly")
	if argon == nil || used(t, argon) != 1 || argon.Scope != "gemini-4-argon" || !argon.Important {
		t.Fatalf("argon = %+v", argon)
	}
	if byID(s, "3p-5h").Scope != "3p" {
		t.Fatalf("3p = %+v", byID(s, "3p-5h"))
	}
}

func TestAntigravityOmittedFractionStaysUnknown(t *testing.T) {
	s, err := ParseAntigravity(map[string]any{"buckets": []any{
		map[string]any{"bucketId": "gemini-5h", "window": "5h"},
	}}, now)
	if err != nil || s.Windows[0].Used != nil {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if _, err := ParseAntigravity(map[string]any{}, now); err == nil {
		t.Fatal("no buckets must fail")
	}
}

func TestSyntheticFixture(t *testing.T) {
	s, err := ParseSynthetic(fixture(t, "synthetic-quotas.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if used(t, byID(s, "subscription")) != 0 || byID(s, "subscription").Value != "0/500 used" {
		t.Fatalf("sub = %+v", byID(s, "subscription"))
	}
	w := byID(s, "7d")
	if got := used(t, w); got < 0.84 || got > 0.8401 || w.Value != "16% left" {
		t.Fatalf("weekly = %+v", w)
	}
	if byID(s, "search-1h").Scope != "search" {
		t.Fatal("search scope")
	}
}

func TestSyntheticMeterWithoutNumbersStaysVisibleAndUnknown(t *testing.T) {
	s, err := ParseSynthetic(map[string]any{"subscription": map[string]any{"renewsAt": "2026-10-26T17:22:12Z"}}, now)
	if err != nil || len(s.Windows) != 1 || s.Windows[0].Used != nil || s.Windows[0].ResetAtMs == nil {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func env(token string) quota.Env {
	return quota.Env{Now: func() time.Time { return now }, Auth: func(context.Context) (*quota.Auth, error) {
		if token == "" {
			return nil, nil
		}
		return &quota.Auth{Token: token}, nil
	}}
}

func TestFetchWithoutCredentialMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, fetch := range map[string]func(context.Context, quota.Env) (quota.Snapshot, error){
		"claude": fetchClaude(srv.URL), "synthetic": fetchSynthetic(srv.URL),
	} {
		s, err := fetch(context.Background(), env(""))
		if err != nil || !s.NeedsAuth || s.Error == "" {
			t.Fatalf("%s: %+v %v", name, s, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a signed-out provider made a request")
	}
}

func TestClaudeFetchReplaysA401OnlyForADifferentCredential(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer old" {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10}}`))
	}))
	defer srv.Close()
	tokens := []string{"old", "new"}
	e := quota.Env{Now: func() time.Time { return now }, Auth: func(context.Context) (*quota.Auth, error) {
		t := tokens[0]
		if len(tokens) > 1 {
			tokens = tokens[1:]
		}
		return &quota.Auth{Token: t}, nil
	}}
	s, err := fetchClaude(srv.URL)(context.Background(), e)
	if err != nil || used(t, byID(s, "5h")) != 0.1 || len(seen) != 2 {
		t.Fatalf("snapshot=%+v err=%v seen=%v", s, err, seen)
	}
	seen = nil
	if _, err := fetchClaude(srv.URL)(context.Background(), env("old")); err == nil || len(seen) != 1 {
		t.Fatalf("same credential must not be replayed: err=%v seen=%v", err, seen)
	}
}
