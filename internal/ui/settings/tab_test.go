package settings

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

func snap(t *testing.T, content string) func() *config.Snapshot {
	t.Helper()
	m := config.NewManager(t.TempDir() + "/auth-plus.toml")
	_ = m.Load()
	return m.Snapshot
}

func selectKey(t *testing.T, tb *Tab, key string) {
	t.Helper()
	for i, d := range config.Catalog {
		if d.Key == key {
			tb.sel = i
			return
		}
	}
	t.Fatalf("no setting %s", key)
}

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	tb := New(snap(t, ""), Actions{Preview: func(string, int) ([]string, []string) {
		return []string{"claude: 5h/27%"}, []string{"{bogus:x}"}
	}}, "quotas.lineTemplate")
	tb.SetNotice("saved to user; higher scope overrides: quotas.linePlacement, tokens.errorPercent", true)
	for _, w := range []int{36, 57, 80, 140} {
		for _, row := range tb.Body(frame.Env{Width: w, Rows: 30}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: %q is %d wide", w, row, tui.VisibleWidth(row))
			}
		}
	}
}

func TestStagingChangesNothingUntilSaved(t *testing.T) {
	get := snap(t, "")
	var saved []config.Edit
	tb := New(get, Actions{Save: func(e []config.Edit) { saved = e }}, "")
	selectKey(t, tb, "quotas.refreshOnRequest")
	tb.Key(" ")
	if !tb.Dirty() {
		t.Fatal("a toggle must stage a draft")
	}
	if got := get().Value("quotas.refreshOnRequest"); got != true {
		t.Fatalf("snapshot changed before save: %v", got)
	}
	tb.Key("\x13") // ctrl+s
	if len(saved) != 1 || saved[0].Key != "quotas.refreshOnRequest" || saved[0].Value != true {
		t.Fatalf("save got %+v", saved)
	}
	tb.Saved([]string{"quotas.refreshOnRequest"})
	if tb.Dirty() {
		t.Fatal("saved edits must leave no draft")
	}
}

func TestIntegerEditingClampsAndRejectsText(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.refreshIntervalSec")
	tb.Key("\r")
	tb.Key("\x15") // ctrl+u clears
	for _, c := range "5000" {
		tb.Key(string(c))
	}
	tb.Key("\r")
	if e := tb.Staged(); len(e) != 1 || e[0].Value != int64(3600) {
		t.Fatalf("expected clamp to 3600, got %+v", e)
	}
	if !strings.Contains(tb.notice, "clamped") {
		t.Fatalf("clamping must be reported: %q", tb.notice)
	}
	tb.Key("\r")
	tb.Key("\x15")
	tb.Key("x")
	tb.Key("\r")
	if tb.editing == nil || !strings.Contains(tb.notice, "integer") {
		t.Fatal("non-numeric input must keep the editor open with an error")
	}
}

func TestBackspaceStagesBuiltInDefault(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.linePlacement")
	tb.Key("\x7f")
	e := tb.Staged()
	if len(e) != 1 || !e[0].Reset {
		t.Fatalf("expected a staged reset, got %+v", e)
	}
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 40}), "\n")
	if !strings.Contains(body, "staged reset") {
		t.Fatalf("a staged reset must be labelled:\n%s", body)
	}
}

func TestPlainTextNamesSource(t *testing.T) {
	out := PlainText(snap(t, "")(), 80, "tokens.errorPercent")
	if !strings.Contains(out, "tokens.errorPercent = 90  (default)") {
		t.Fatal(out)
	}
}

func open(t *testing.T, key string) *Tab {
	t.Helper()
	tb := New(snap(t, ""), Actions{Known: func(string) []string { return []string{"claude", "codex", "xai"} }}, key)
	tb.Key("\r")
	if tb.coll == nil {
		t.Fatalf("%s must open a collection editor", key)
	}
	return tb
}

func typeText(tb *Tab, s string) {
	for _, c := range s {
		tb.Key(string(c))
	}
}

func TestProviderListStagesCheckedAndCustomIDs(t *testing.T) {
	tb := open(t, "quotas.providers")
	tb.Key(" ") // claude
	tb.Key("a")
	typeText(tb, "my-proxy")
	tb.Key("\r")
	tb.Key("\x13")
	e := tb.Staged()
	got, _ := e[0].Value.([]string)
	if tb.coll != nil || len(e) != 1 || strings.Join(got, ",") != "claude,my-proxy" {
		t.Fatalf("staged %+v, editor open=%v", e, tb.coll != nil)
	}
}

func TestEscOnDirtyEditorNeedsConfirmation(t *testing.T) {
	tb := open(t, "claude.cacheRetentionProviders")
	tb.Key(" ")
	if !tb.Dirty() {
		t.Fatal("a changed draft must count as unsaved")
	}
	tb.Key("\x1b")
	if tb.coll == nil {
		t.Fatal("the first Esc must not discard a draft")
	}
	tb.Key("\x1b")
	if tb.coll != nil || len(tb.Staged()) != 0 {
		t.Fatal("the second Esc discards and stages nothing")
	}
}

func TestPatternRulesAddEditReorderDrop(t *testing.T) {
	tb := open(t, "claude.replacePatterns")
	tb.Key("a")
	typeText(tb, "old")
	tb.Key("\r")
	typeText(tb, "new")
	tb.Key("\r")
	tb.Key("a")
	typeText(tb, "secret")
	tb.Key("\r")
	tb.Key("\r") // empty replacement: drop paragraphs
	tb.Key("\x1b[1;2A")
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n")
	if !strings.Contains(body, `1. drop paragraphs containing "secret"`) || !strings.Contains(body, `2. "old" → "new"`) {
		t.Fatalf("rules not ordered as edited:\n%s", body)
	}
	tb.Key("\x13")
	rules, _ := tb.Staged()[0].Value.([]config.ReplacePattern)
	if len(rules) != 2 || rules[0].HasReplacement || !rules[1].HasReplacement {
		t.Fatalf("got %+v", rules)
	}
}

func TestCollectionEditorsFitAllWidths(t *testing.T) {
	for _, key := range []string{"quotas.providers", "claude.replacePatterns"} {
		tb := open(t, key)
		tb.Key("a")
		typeText(tb, strings.Repeat("long-input ", 10))
		for _, w := range []int{36, 57, 80, 140} {
			for _, row := range tb.Body(frame.Env{Width: w, Rows: 20}) {
				if tui.VisibleWidth(row) > w {
					t.Fatalf("%s width %d: %q", key, w, row)
				}
			}
		}
	}
}
