package tokens

import (
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

func TestLinesFitEveryWidth(t *testing.T) {
	b := Breakdown{Parts: []tokens.Part{{Kind: tokens.PartSystem, Count: tokens.Count{Tokens: 6000, Source: tokens.SourceEstimate}, Items: 1}}}
	for _, w := range []int{36, 57, 80, 140} {
		for _, line := range Lines(plain{}, sample(), b, Display{WarningPercent: 70, ErrorPercent: 90}, w) {
			if tui.VisibleWidth(line) > w {
				t.Fatalf("width %d: %q is %d wide", w, line, tui.VisibleWidth(line))
			}
		}
	}
}

func TestLinesStateIsNeverColorAlone(t *testing.T) {
	text := strings.Join(Lines(plain{}, sample(), Breakdown{}, Display{WarningPercent: 70, ErrorPercent: 90}, 140), "\n")
	for _, want := range []string{"✗", "92%", "479,000", "estimate", "delta +359,000", "run /login claude"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestPlainTextWithoutReport(t *testing.T) {
	if got := PlainText(nil, Display{}, 80); !strings.Contains(got, "No context count") {
		t.Fatal(got)
	}
}
