package models

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

func catalog() []Model {
	return []Model{
		{Provider: "claude", ID: "claude-opus-5-5", Name: "Claude Opus 5.5", API: "anthropic-messages", ContextWindow: 425000, MaxTokens: 32000, Reasoning: true, Input: []string{"text", "image"}, Cost: Cost{Input: 5, Output: 25}},
		{Provider: "claude", ID: "fast", Name: "Fast", AliasOf: "claude-haiku-5-5", ContextWindow: 200000, Overridden: []string{"contextWindow"}},
		{Provider: "synthetic", ID: "hf:deepseek-ai/DeepSeek-V4.1-Flash-1017", Name: "DeepSeek V4.1 Flash \x1b[31mred\x1b[0m", ContextWindow: 524288, MaxTokens: 65536},
		{Provider: "xai", ID: "grok-4.7", ContextWindow: 1_000_000},
	}
}

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		tb := New(catalog, func() string { return "" }, Actions{})
		for _, row := range tb.Body(frame.Env{Width: w, Rows: 20}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("list width %d: %q", w, row)
			}
		}
		tb.Key("j")
		tb.Key("\r")
		for _, row := range tb.Body(frame.Env{Width: w, Rows: 20}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("detail width %d: %q", w, row)
			}
		}
	}
}

func TestSearchNarrowsAcrossProviderIDAndName(t *testing.T) {
	tb := New(catalog, func() string { return "" }, Actions{})
	tb.Key("/")
	for _, c := range "synthetic deepseek" {
		tb.Key(string(c))
	}
	tb.Key("\r")
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n")
	if !strings.Contains(body, "DeepSeek-V4.1") || strings.Contains(body, "grok-4.7") {
		t.Fatalf("filter wrong:\n%s", body)
	}
	tb.Key("\x1b")
	if body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n"); !strings.Contains(body, "grok-4.7") {
		t.Fatal("Esc clears the filter")
	}
}

func TestDetailShowsLimitsPricesAndAliasOrigin(t *testing.T) {
	tb := New(catalog, func() string { return "" }, Actions{})
	tb.Key("\r") // claude-opus-5-5
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n")
	for _, want := range []string{"claude/claude-opus-5-5", "425k", "yes", "text, image", "in $5", "out $25"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail lacks %q:\n%s", want, body)
		}
	}
	tb.Key("\x1b")
	tb.Key("j")
	tb.Key("\r")
	body = strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n")
	if strings.Contains(body, "claude/claude-haiku-5-5") == false || !strings.Contains(body, "alias of") || !strings.Contains(body, "contextWindow") {
		t.Fatalf("alias origin and override missing:\n%s", body)
	}
}

func TestUntrustedNamesAreStripped(t *testing.T) {
	tb := New(catalog, func() string { return "" }, Actions{})
	tb.Key("/")
	tb.Key("d")
	tb.Key("\r")
	tb.Key("\r")
	if body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 20}), "\n"); strings.Contains(body, "\x1b[31m") {
		t.Fatal("escape sequence from a model name reached the screen")
	}
}

func TestPlainTextCountsPerProvider(t *testing.T) {
	out := PlainText(catalog(), 80)
	if !strings.Contains(out, "4 models in 3 providers") || !strings.Contains(out, "claude  2") {
		t.Fatal(out)
	}
}

func TestFailureIsShownNotEndlessLoading(t *testing.T) {
	tb := New(func() []Model { return nil }, func() string { return "host unavailable" }, Actions{})
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 10}), "\n")
	if !strings.Contains(body, "could not be read: host unavailable") {
		t.Fatal(body)
	}
}

func TestLongIDsKeepTheirTail(t *testing.T) {
	long := []Model{{Provider: "bedrock", ID: "anthropic.claude-haiku-4-5-20251001-v1:0", ContextWindow: 200000}}
	tb := New(func() []Model { return long }, func() string { return "" }, Actions{})
	body := strings.Join(tb.Body(frame.Env{Width: 36, Rows: 10}), "\n")
	if !strings.Contains(body, "v1:0") {
		t.Fatalf("the distinguishing tail was cut:\n%s", body)
	}
}

func overrideTab(t *testing.T, saved *[]modelsfile.Op) *Tab {
	t.Helper()
	m := Model{Provider: "claude", ID: "claude-opus-5", Name: "Opus 5", ContextWindow: 1_000_000, MaxTokens: 64000, Input: []string{"text"}}
	tb := New(func() []Model { return []Model{m} }, func() string { return "" }, Actions{
		SaveOverride: func(_ Model, ops []modelsfile.Op) { *saved = ops },
	})
	tb.Body(frame.Env{Width: 80, Rows: 20})
	tb.Key("\r") // detail
	tb.Key("o")
	if tb.ov == nil {
		t.Fatal("o must open the override editor")
	}
	return tb
}

func TestOverrideStagesAndSavesInOneWrite(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	tb.Key("\x1b[B") // context window
	tb.Key("\r")
	tb.Key("\x15")
	for _, c := range "425000" {
		tb.Key(string(c))
	}
	tb.Key("\r")
	for range 5 {
		tb.Key("\x1b[B") // price in
	}
	tb.Key("\r")
	tb.Key("\x15")
	for _, c := range "2.5" {
		tb.Key(string(c))
	}
	tb.Key("\r")
	if !tb.Dirty() {
		t.Fatal("staged fields must count as unsaved")
	}
	tb.Key("\x13")
	if len(saved) != 2 || saved[0].Path[0] != "contextWindow" || saved[0].Value != int64(425000) || saved[1].Value != 2.5 {
		t.Fatalf("got %+v", saved)
	}
}

func TestOverrideRejectsInvalidNumbersAndAsksBeforeDiscarding(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	tb.Key("\x1b[B")
	tb.Key("\r")
	tb.Key("\x15")
	tb.Key("x")
	tb.Key("\r")
	if !tb.ov.editing || len(tb.ov.changes) != 0 {
		t.Fatal("a non-number must keep the editor open and stage nothing")
	}
	tb.Key("\x1b") // leave the input
	tb.Key(" ")    // not on a toggle; stage via backspace instead
	tb.Key("\x1b[B")
	tb.Key("\x1b[B") // input types
	tb.Key(" ")
	if !tb.Dirty() {
		t.Fatal("toggling image input must stage a change")
	}
	tb.Key("\x1b")
	if tb.ov == nil {
		t.Fatal("the first Esc must not discard staged fields")
	}
	tb.Key("\x1b")
	if tb.ov != nil || tb.Dirty() {
		t.Fatal("the second Esc discards")
	}
}

func TestOverrideRemovalOnlyForOverriddenFields(t *testing.T) {
	var saved []modelsfile.Op
	tb := overrideTab(t, &saved)
	tb.ov.model.Overridden = []string{"contextWindow"}
	tb.Key("\x1b[B")
	tb.Key("\x7f")
	tb.Key("\x1b[B")
	tb.Key("\x7f") // maxTokens is not overridden: nothing to remove
	tb.Key("\x13")
	if len(saved) != 1 || !saved[0].Remove || saved[0].Path[0] != "contextWindow" {
		t.Fatalf("got %+v", saved)
	}
}

func typeIn(tb *Tab, s string) {
	for _, c := range s {
		tb.Key(string(c))
	}
}

func openDetail(t *testing.T, tb *Tab, key string) {
	t.Helper()
	tb.Key("/")
	typeIn(tb, key)
	tb.Key("\r")
	tb.Key("\r")
	if tb.detail == nil || tb.detail.Key() != key {
		t.Fatalf("detail is %+v", tb.detail)
	}
}

func TestNewAliasIsValidatedThenSaved(t *testing.T) {
	var saved Alias
	var old *Model
	tb := New(catalog, func() string { return "" }, Actions{SaveAlias: func(o *Model, a Alias) { old, saved = o, a }})
	openDetail(t, tb, "claude/claude-opus-5-5")
	tb.Key("a")
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
	tb.Key("\x1b[B")
	tb.Key("\x1b[B")
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
	tb := New(catalog, func() string { return "" }, Actions{
		Alias: func(m Model) (Alias, bool) {
			return Alias{Provider: "claude", Source: "claude-haiku-5-5", ID: "fast"}, true
		},
		SaveAlias: func(o *Model, a Alias) { old = o },
	})
	openDetail(t, tb, "claude/fast")
	tb.Key("a")
	if tb.al == nil || tb.al.isNew {
		t.Fatal("an alias opens its own definition")
	}
	tb.Key("\x1b[B")
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

func TestAliasFormFitsAllWidths(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		tb := New(catalog, func() string { return "" }, Actions{})
		openDetail(t, tb, "synthetic/hf:deepseek-ai/DeepSeek-V4.1-Flash-1017")
		tb.Key("a")
		tb.Key("\x13")
		for _, row := range tb.Body(frame.Env{Width: w, Rows: 20}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: %q", w, row)
			}
		}
	}
}
