package tokens

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

func sample() *tokens.Report {
	pct, host := 91.5, 120000
	return &tokens.Report{
		Provider: "synthetic", Model: "hf:deepseek-ai/DeepSeek-V4.1-Flash-1017", ContextWindow: 524288,
		Tokens: tokens.Count{Tokens: 479_000, Source: tokens.SourceEstimate, Diagnostics: []tokens.Diagnostic{{Provider: "claude", Message: "run /login claude"}}}, Percent: &pct, HostTokens: &host,
		Anchor: &tokens.Count{Tokens: 470_000, Source: tokens.SourceProvider},
		Tail:   tokens.Count{Tokens: 9_000, Source: tokens.SourceEstimate}, TailMessages: 3,
	}
}

func breakdown() Breakdown {
	return Breakdown{Parts: []tokens.Part{
		{Kind: tokens.PartSystem, Count: tokens.Count{Tokens: 7204, Source: tokens.SourceEstimate}, Items: 3},
		{Kind: tokens.PartTools, Count: tokens.Count{Tokens: 12880, Source: tokens.SourceEstimate}, Items: 41},
		{Kind: tokens.PartUser, Count: tokens.Count{Tokens: 28127, Source: tokens.SourceProvider}, Items: 1},
	}}
}

func TestLinesFitEveryWidthAndKeepTheCountVisible(t *testing.T) {
	for _, w := range []int{36, 57, 80, 140} {
		lines := Lines(plain{}, sample(), breakdown(), Display{WarningPercent: 70, ErrorPercent: 90}, w)
		for _, line := range lines {
			if tui.VisibleWidth(line) > w {
				t.Fatalf("width %d: %q is %d wide", w, line, tui.VisibleWidth(line))
			}
		}
		if !strings.Contains(lines[0], "479") {
			t.Fatalf("width %d: the count was cut from %q", w, lines[0])
		}
	}
}

func TestContextRowShowsBarShareWindowAndCountWhenWide(t *testing.T) {
	lines := Lines(plain{}, sample(), breakdown(), Display{WarningPercent: 70, ErrorPercent: 90}, 100)
	for _, want := range []string{"▕", "✗ 91.5% of 524K", "479,000 tokens"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("missing %q in %q", want, lines[0])
		}
	}
}

func TestEverySourceHasAGlyphAndAWord(t *testing.T) {
	text := strings.Join(Lines(plain{}, sample(), breakdown(), Display{}, 140), "\n")
	for _, want := range []string{"≈ estimate", "● exact (usage)", "Δ +359,000 (+299.2%) vs Pi's estimate", "run /login claude", "41 tools", "1 message"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestBreakdownSharesAddUpToTheRequestNotTheWindow(t *testing.T) {
	var shares int
	for _, line := range Lines(plain{}, sample(), breakdown(), Display{}, 140) {
		if strings.Contains(line, "tools ") || strings.HasPrefix(strings.TrimSpace(line), "system") || strings.HasPrefix(strings.TrimSpace(line), "user") {
			for _, f := range strings.Fields(line) {
				if strings.HasSuffix(f, "%") {
					var n int
					if _, err := fmt.Sscanf(f, "%d%%", &n); err == nil {
						shares += n
					}
				}
			}
		}
	}
	if shares < 99 || shares > 101 {
		t.Fatalf("shares sum to %d%%", shares)
	}
}

func TestUnknownWindowShowsNoBarAndNoPercent(t *testing.T) {
	r := sample()
	r.Percent, r.ContextWindow = nil, 0
	line := Lines(plain{}, r, Breakdown{}, Display{}, 100)[0]
	if strings.Contains(line, "▕") || strings.Contains(line, "%") || !strings.Contains(line, "window unknown") {
		t.Fatalf("%q", line)
	}
}

func TestBreakdownStatesAreWordsNotBlanks(t *testing.T) {
	if got := strings.Join(Lines(plain{}, sample(), Breakdown{Loading: true}, Display{}, 80), "\n"); !strings.Contains(got, "fetching…") {
		t.Fatal(got)
	}
	if got := strings.Join(Lines(plain{}, sample(), Breakdown{Failed: true}, Display{}, 80), "\n"); !strings.Contains(got, "unavailable") {
		t.Fatal(got)
	}
}

func TestPlainTextWithoutReport(t *testing.T) {
	if got := PlainText(nil, Display{}, 80); !strings.Contains(got, "No context count") {
		t.Fatal(got)
	}
}
