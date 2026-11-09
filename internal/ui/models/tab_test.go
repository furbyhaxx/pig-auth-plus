package models

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

var env = frame.Env{Width: 100, Rows: 12}

func catalog() []Model {
	return []Model{
		{Provider: "claude", ID: "claude-opus-5-5", Name: "Claude Opus 5.5", API: "anthropic-messages", ContextWindow: 425000, MaxTokens: 32000, Reasoning: true, Input: []string{"text", "image"}, Cost: Cost{Input: 5, Output: 25}, SignedIn: true},
		{Provider: "claude", ID: "fast", Name: "Fast", AliasOf: "claude-haiku-5-5", ContextWindow: 200000, Overridden: []string{"contextWindow"}, SignedIn: true},
		{Provider: "synthetic", ID: "hf:deepseek-ai/DeepSeek-V4.1-Flash-1017", Name: "DeepSeek V4.1 Flash \x1b[31mred\x1b[0m", ContextWindow: 524288, MaxTokens: 65536, SignedIn: true},
		{Provider: "xai", ID: "grok-4.7", ContextWindow: 1_000_000, SignedIn: true},
		{Provider: "opencode-go", ID: "kimi", ContextWindow: 128000},
	}
}

func newTab(a Actions) *Tab { return New(catalog, func() string { return "" }, a) }

func body(tb *Tab) string { return strings.Join(tb.Body(env), "\n") }

func typeIn(tb *Tab, s string) {
	for _, c := range s {
		tb.Key(string(c))
	}
}

// open selects key through the frame's filter and opens it.
func open(t *testing.T, tb *Tab, key string) {
	t.Helper()
	tb.SetFilter(key)
	tb.Body(env)
	tb.Key("\r")
	tb.SetFilter("")
	tb.Body(env)
}

func TestEveryScreenFitsAtAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		e := frame.Env{Width: w, Rows: 12}
		check := func(what string, tb *Tab) {
			for _, row := range tb.Body(e) {
				if tui.VisibleWidth(row) > w {
					t.Fatalf("%s at %d: %q is %d wide", what, w, row, tui.VisibleWidth(row))
				}
			}
		}
		tb := newTab(Actions{Alias: func(Model) (Alias, bool) {
			return Alias{Provider: "claude", Source: "claude-haiku-5-5", ID: "fast"}, true
		}})
		check("list", tb)
		open(t, tb, "claude/claude-opus-5-5")
		check("detail", tb)
		tb.Key("\x1b")
		open(t, tb, "claude/fast")
		check("alias", tb)
	}
}

func TestCatalogListsOnlySignedInProvidersAndSaysWhatItLeftOut(t *testing.T) {
	tb := newTab(Actions{})
	got := body(tb)
	if strings.Contains(got, "opencode-go/kimi") {
		t.Fatalf("a signed-out provider's model is listed:\n%s", got)
	}
	if !strings.Contains(got, "grok-4.7") {
		t.Fatalf("signed-in model missing:\n%s", got)
	}
	if s := tb.Status(env); !strings.Contains(s, "4 models") || !strings.Contains(s, "1 providers left out") {
		t.Fatalf("status %q", s)
	}
}

func TestFilterNarrowsAcrossProviderIDAndName(t *testing.T) {
	tb := newTab(Actions{})
	tb.SetFilter("synthetic deepseek")
	if got := body(tb); !strings.Contains(got, "DeepSeek-V4.1") || strings.Contains(got, "grok-4.7") {
		t.Fatalf("filter wrong:\n%s", got)
	}
	tb.SetFilter("zzzz")
	if got := body(tb); !strings.Contains(got, "no model matches") {
		t.Fatalf("empty result not explained:\n%s", got)
	}
}

func TestEnterOpensTheDetailAndEscGoesBackToTheSameRow(t *testing.T) {
	tb := newTab(Actions{})
	tb.Body(env)
	tb.Key("\x1b[B") // fast
	tb.Key("\x1b[B") // synthetic
	tb.Body(env)
	selected := tb.cat.Selected()
	tb.Key("\r")
	if got := tb.Crumbs(); len(got) != 1 || got[0] != selected {
		t.Fatalf("crumbs %v, want %q", got, selected)
	}
	if tb.Filterable() {
		t.Fatal("a detail screen takes no filter text")
	}
	if !tb.Key("\x1b") {
		t.Fatal("esc in a detail belongs to the tab, otherwise the dashboard closes")
	}
	tb.Body(env)
	if tb.Crumbs() != nil || tb.cat.Selected() != selected {
		t.Fatalf("back must keep the row: %v %q", tb.Crumbs(), tb.cat.Selected())
	}
}

func TestDetailShowsEffectiveValuesWithTheirSource(t *testing.T) {
	tb := newTab(Actions{})
	open(t, tb, "claude/claude-opus-5-5")
	got := body(tb)
	for _, want := range []string{"Context window", "425,000", "provider", "Reasoning", "on", "text, image", "$5", "+ New alias"} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail lacks %q:\n%s", want, got)
		}
	}
}

func TestAliasModelOpensItsOwnDefinitionWithRemove(t *testing.T) {
	var removed Model
	tb := newTab(Actions{
		Alias: func(Model) (Alias, bool) {
			return Alias{Provider: "claude", Source: "claude-haiku-5-5", ID: "fast"}, true
		},
		RemoveAlias: func(m Model) { removed = m },
	})
	open(t, tb, "claude/fast")
	if tb.al == nil || tb.al.isNew || tb.ov != nil {
		t.Fatalf("alias must open the alias screen directly: al=%v ov=%v", tb.al, tb.ov)
	}
	got := body(tb)
	for _, want := range []string{"Alias ID", "claude/claude-haiku-5-5", "inherited from the source", "Remove alias"} {
		if !strings.Contains(got, want) {
			t.Fatalf("alias screen lacks %q:\n%s", want, got)
		}
	}
	for range 5 {
		tb.Key("\x1b[B")
		tb.Body(env)
	}
	tb.Key("\r")
	if removed.Key() != "claude/fast" {
		t.Fatalf("remove asked for %q", removed.Key())
	}
}

func overrideTab(t *testing.T, saved *[]modelsfile.Op, overridden ...string) *Tab {
	t.Helper()
	m := Model{Provider: "claude", ID: "claude-opus-5", Name: "Opus 5", ContextWindow: 1_000_000, MaxTokens: 64000, Input: []string{"text"}, SignedIn: true, Overridden: overridden}
	tb := New(func() []Model { return []Model{m} }, func() string { return "" }, Actions{
		SaveOverride: func(_ Model, ops []modelsfile.Op) { *saved = ops },
	})
	tb.Body(env)
	tb.Key("\r")
	if tb.ov == nil {
		t.Fatal("enter must open the detail")
	}
	return tb
}

func down(tb *Tab, n int) {
	for range n {
		tb.Body(env)
		tb.Key("\x1b[B")
	}
	tb.Body(env)
}

func TestOverrideStagesAndSavesInOneWrite(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	down(tb, 1) // context window
	tb.Key("\r")
	tb.Key("\x15")
	typeIn(tb, "425000")
	tb.Key("\r")
	down(tb, 4) // price in
	tb.Key("\r")
	tb.Key("\x15")
	typeIn(tb, "2.5")
	tb.Key("\r")
	if !tb.Dirty() {
		t.Fatal("staged fields must count as unsaved")
	}
	if !strings.Contains(body(tb), "● staged") {
		t.Fatalf("a staged field must say so:\n%s", body(tb))
	}
	tb.Key("\x13")
	if len(saved) != 2 || saved[0].Path[0] != "contextWindow" || saved[0].Value != int64(425000) || saved[1].Value != 2.5 {
		t.Fatalf("got %+v", saved)
	}
}

func TestOverrideRejectsInvalidNumbersAndAsksBeforeDiscarding(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	down(tb, 1)
	tb.Key("\r")
	tb.Key("\x15")
	typeIn(tb, "x")
	tb.Key("\r")
	if !tb.ov.in.on || len(tb.ov.changes) != 0 {
		t.Fatal("a non-number must keep the input open and stage nothing")
	}
	tb.Key("\x1b") // leave the input
	down(tb, 3)    // reasoning, input
	tb.Key(" ")
	if !tb.Dirty() {
		t.Fatal("toggling image input must stage a change")
	}
	tb.Key("\x1b")
	if tb.ov == nil || !strings.Contains(tb.Description(env), "Esc again") {
		t.Fatal("the first Esc must warn and keep the staged fields")
	}
	tb.Key("\x1b")
	if tb.ov != nil || tb.Dirty() {
		t.Fatal("the second Esc discards")
	}
}

func TestOverrideRemovalOnlyForOverriddenFields(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved, "contextWindow")
	down(tb, 1)
	tb.Key("\x7f")
	down(tb, 1)
	tb.Key("\x7f") // max tokens is not overridden: nothing to remove
	tb.Key("\x13")
	if len(saved) != 1 || !saved[0].Remove || saved[0].Path[0] != "contextWindow" {
		t.Fatalf("got %+v", saved)
	}
}

func TestLeftRightSwitchTabsOnRowsThatDoNotUseThem(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	down(tb, len(ovFields)) // the aliases info row
	if tb.Key("\x1b[C") {
		t.Fatal("right on a read-only row must reach the frame so it can switch tabs")
	}
}

func TestNewAliasIsValidatedThenSaved(t *testing.T) {
	var saved Alias
	var old *Model
	tb := newTab(Actions{SaveAlias: func(o *Model, a Alias) { old, saved = o, a }})
	open(t, tb, "claude/claude-opus-5-5")
	down(tb, len(ovFields)+1)
	tb.Key("\r") // + New alias
	if tb.al == nil || !tb.al.isNew {
		t.Fatal("the + New alias row must open the alias form")
	}
	if got := tb.Crumbs(); len(got) != 2 || got[1] != "New alias" {
		t.Fatalf("crumbs %v", got)
	}
	tb.Key("\x13")
	if saved.ID != "" || !strings.Contains(tb.al.err, "needs an id") {
		t.Fatalf("an empty id must be refused: %q", tb.al.err)
	}
	tb.Key("\r")
	typeIn(tb, "fast") // taken by the catalog's alias
	tb.Key("\r")
	tb.Key("\x13")
	if !strings.Contains(tb.al.err, "already names a model") {
		t.Fatalf("collision must be refused: %q", tb.al.err)
	}
	tb.Key("\r")
	tb.Key("\x15")
	typeIn(tb, "opus-lite")
	tb.Key("\r")
	down(tb, 3) // context window
	tb.Key("\r")
	typeIn(tb, "300000")
	tb.Key("\r")
	tb.Key("\x13")
	if old != nil || saved.ID != "opus-lite" || saved.Source != "claude-opus-5-5" || saved.Provider != "claude" || saved.ContextWindow != 300000 || saved.MaxTokens != 0 {
		t.Fatalf("old=%v saved=%+v", old, saved)
	}
}

func TestEditingAnAliasKeepsItsIdentityAndDirtyEscAsks(t *testing.T) {
	var old *Model
	tb := newTab(Actions{
		Alias: func(Model) (Alias, bool) {
			return Alias{Provider: "claude", Source: "claude-haiku-5-5", ID: "fast"}, true
		},
		SaveAlias: func(o *Model, a Alias) { old = o },
	})
	open(t, tb, "claude/fast")
	down(tb, 2) // display name
	tb.Key("\r")
	typeIn(tb, "Quick")
	tb.Key("\r")
	if !tb.Dirty() {
		t.Fatal("a changed form is a draft")
	}
	tb.Key("\x1b")
	if tb.al == nil {
		t.Fatal("the first Esc must keep the draft")
	}
	tb.Key("\x13")
	if old == nil || old.Key() != "claude/fast" {
		t.Fatalf("the saved alias must name the entry it replaces: %v", old)
	}
}

func TestUntrustedNamesAreStripped(t *testing.T) {
	tb := newTab(Actions{})
	tb.SetFilter("deepseek")
	tb.Body(env)
	tb.Key("\r")
	if got := body(tb) + tb.Description(env); strings.Contains(got, "\x1b[31m") {
		t.Fatal("escape sequence from a model name reached the screen")
	}
}

func TestFailureIsShownNotEndlessLoading(t *testing.T) {
	tb := New(func() []Model { return nil }, func() string { return "host unavailable" }, Actions{})
	if got := body(tb); !strings.Contains(got, "could not be read: host unavailable") {
		t.Fatal(got)
	}
}

func TestLongIDsKeepTheirTail(t *testing.T) {
	long := []Model{{Provider: "bedrock", ID: "anthropic.claude-haiku-4-5-20251001-v1:0", ContextWindow: 200000, SignedIn: true}}
	tb := New(func() []Model { return long }, func() string { return "" }, Actions{})
	got := strings.Join(tb.Body(frame.Env{Width: 36, Rows: 10}), "\n")
	if !strings.Contains(got, "v1:0") {
		t.Fatalf("the distinguishing tail was cut:\n%s", got)
	}
}

func TestPlainTextCountsSignedInModelsAndNamesTheRest(t *testing.T) {
	out := PlainText(catalog(), 80)
	if !strings.Contains(out, "4 models in 3 signed-in providers") || !strings.Contains(out, "claude  2") || !strings.Contains(out, "left out: opencode-go") {
		t.Fatal(out)
	}
}
