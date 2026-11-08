package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T, content string) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth-plus.toml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(path)
	_ = m.Load()
	return m, path
}

func TestSaveKeepsCommentsAndUnknownKeys(t *testing.T) {
	m, path := newTestManager(t, "# my notes\n[quotas]\n# placement\nlinePlacement = \"off\" # trailing\nfuture = 3\n")
	if w := m.Snapshot().Warnings; len(w) != 1 || !strings.Contains(w[0], "quotas.future") {
		t.Fatalf("warnings %v", w)
	}
	if _, err := m.Save(context.Background(), ScopeUser, []Edit{
		{Key: "quotas.percentDisplay", Value: "remaining"},
		{Key: "claude.replacePatterns", Value: []ReplacePattern{{Match: `a "b"`}, {Match: "x", Replacement: "", HasReplacement: true}}},
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"# my notes", "# placement", "# trailing", "future = 3", `percentDisplay = "remaining"`, `[[claude.replacePatterns]]`} {
		if !strings.Contains(text, want) {
			t.Errorf("saved file lacks %q:\n%s", want, text)
		}
	}
	reloaded := NewManager(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := reloaded.Snapshot().ReplacePatterns
	if len(got) != 2 || got[0].Match != `a "b"` || got[0].HasReplacement || !got[1].HasReplacement {
		t.Errorf("patterns did not round-trip: %+v", got)
	}
}

func TestMalformedFileBlocksUserSave(t *testing.T) {
	m, path := newTestManager(t, "[quotas\n")
	if len(m.Snapshot().Warnings) == 0 {
		t.Fatal("malformed file must be reported")
	}
	if _, err := m.Save(context.Background(), ScopeUser, []Edit{{Key: "quotas.alwaysShortIds", Value: true}}); err == nil {
		t.Fatal("save over a malformed file must fail")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "[quotas\n" {
		t.Errorf("malformed file was rewritten: %q", data)
	}
}

func TestConcurrentSavesKeepDisjointEdits(t *testing.T) {
	_, path := newTestManager(t, "")
	keys := []string{"quotas.alwaysShortIds", "quotas.hideSignedOut", "dashboard.showProvenance", "quotas.refreshOnRequest"}
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := NewManager(path)
			_ = m.Load()
			if _, err := m.Save(context.Background(), ScopeUser, []Edit{{Key: key, Value: i%2 == 0}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	m := NewManager(path)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if m.Snapshot().Source(key) != ScopeUser {
			t.Errorf("%s lost by a concurrent save", key)
		}
	}
}

func TestRelationRejectedOnSaveAndInheritedOnLoad(t *testing.T) {
	m, _ := newTestManager(t, "[tokens]\nwarningPercent = 95\n")
	s := m.Snapshot()
	if s.TokensWarningPercent != 70 || s.TokensErrorPercent != 90 {
		t.Errorf("contradictory loaded pair must inherit defaults, got %d/%d", s.TokensWarningPercent, s.TokensErrorPercent)
	}
	if len(s.Warnings) == 0 {
		t.Error("contradiction must warn")
	}
	if _, err := m.Save(context.Background(), ScopeSession, []Edit{{Key: "quotas.creditCriticalUsd", Value: int64(60)}}); err == nil {
		t.Error("save that contradicts creditHealthyUsd must fail")
	}
	if _, err := m.Save(context.Background(), ScopeSession, []Edit{{Key: "tokens.errorPercent", Value: int64(99)}}); err != nil {
		t.Fatalf("session save resolving the pair: %v", err)
	}
	if s := m.Snapshot(); s.TokensWarningPercent != 95 || s.Source("tokens.errorPercent") != ScopeSession {
		t.Errorf("session value should make the user value valid, got %d", s.TokensWarningPercent)
	}
}

func TestSessionShadowsUserSave(t *testing.T) {
	m, _ := newTestManager(t, "")
	ctx := context.Background()
	if _, err := m.Save(ctx, ScopeSession, []Edit{{Key: "openai.verbosity", Value: "low"}}); err != nil {
		t.Fatal(err)
	}
	res, err := m.Save(ctx, ScopeUser, []Edit{{Key: "openai.verbosity", Value: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Shadowed) != 1 || m.Snapshot().Verbosity != "low" {
		t.Errorf("shadowed %v, effective %q", res.Shadowed, m.Snapshot().Verbosity)
	}
	if _, err := m.Save(ctx, ScopeSession, []Edit{{Key: "openai.verbosity", Reset: true}}); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Verbosity != "high" {
		t.Errorf("reset session value should reveal user value, got %q", m.Snapshot().Verbosity)
	}
}

func TestInvalidValuesWarnAndClamp(t *testing.T) {
	m, _ := newTestManager(t, "[quotas]\nrefreshIntervalSec = 0\nlinePlacement = \"middle\"\n[[models.aliases]]\nprovider = \"x\"\n")
	s := m.Snapshot()
	if s.RefreshIntervalSec != 15 {
		t.Errorf("interval 0 clamps to 15, got %d", s.RefreshIntervalSec)
	}
	if s.LinePlacement != "below-editor-first" || len(s.Aliases) != 0 {
		t.Errorf("invalid values must inherit: %q %v", s.LinePlacement, s.Aliases)
	}
	if len(s.Warnings) != 3 {
		t.Errorf("want 3 warnings, got %v", s.Warnings)
	}
	if _, err := m.Save(context.Background(), ScopeSession, []Edit{{Key: "quotas.refreshIntervalSec", Value: int64(5)}}); err == nil {
		t.Error("out-of-range save must be rejected, not clamped")
	}
}

func TestAliasesRoundTrip(t *testing.T) {
	m, path := newTestManager(t, "")
	want := []Alias{{Provider: "antigravity", Source: "gemini-4-argon", ID: "fast", Name: "Fast", ContextWindow: 1000}}
	if _, err := m.Save(context.Background(), ScopeUser, []Edit{{Key: "models.aliases", Value: want}}); err != nil {
		t.Fatal(err)
	}
	r := NewManager(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot().Aliases
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %+v", got)
	}
	if _, err := m.Save(context.Background(), ScopeUser, []Edit{{Key: "models.aliases", Reset: true}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "aliases") {
		t.Errorf("reset must remove every alias table:\n%s", data)
	}
}
