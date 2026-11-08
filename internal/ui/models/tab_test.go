package models

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

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
		tb := New(catalog, func() string { return "" })
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
	tb := New(catalog, func() string { return "" })
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
	tb := New(catalog, func() string { return "" })
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
	tb := New(catalog, func() string { return "" })
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
	tb := New(func() []Model { return nil }, func() string { return "host unavailable" })
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 10}), "\n")
	if !strings.Contains(body, "could not be read: host unavailable") {
		t.Fatal(body)
	}
}

func TestLongIDsKeepTheirTail(t *testing.T) {
	long := []Model{{Provider: "bedrock", ID: "anthropic.claude-haiku-4-5-20251001-v1:0", ContextWindow: 200000}}
	tb := New(func() []Model { return long }, func() string { return "" })
	body := strings.Join(tb.Body(frame.Env{Width: 36, Rows: 10}), "\n")
	if !strings.Contains(body, "v1:0") {
		t.Fatalf("the distinguishing tail was cut:\n%s", body)
	}
}
