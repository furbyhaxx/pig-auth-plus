package line

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// plainTheme renders styled text as text with a marker, so tests can assert styling.
type plainTheme struct{}

func (plainTheme) Paint(text string, s Style) string {
	if s.Fg != nil && s.Fg.Hex != "" {
		return "<" + s.Fg.Hex + ">" + text + "</>"
	}
	return text
}

var sources = map[string]*quota.Source{
	"claude":     {ID: "claude", Label: "Claude", Short: "cl", Aliases: []string{"cl", "cc"}},
	"codex":      {ID: "codex", Label: "ChatGPT", Short: "cox", Aliases: []string{"openai", "cx"}},
	"openrouter": {ID: "openrouter", Label: "OpenRouter", Short: "or", Credits: true},
	"xai":        {ID: "xai", Label: "xAI", Short: "xai"},
}

func resolve(n string) string {
	for _, s := range sources {
		if n == s.ID {
			return s.ID
		}
		for _, a := range s.Aliases {
			if n == a {
				return s.ID
			}
		}
	}
	return ""
}

func win(id, period string, used float64) quota.Window {
	return quota.Window{ID: id, Short: id, Period: period, Used: quota.Frac(used), Important: true}
}

func views() []quota.View {
	return []quota.View{
		{Source: sources["claude"], Snapshot: &quota.Snapshot{Windows: []quota.Window{win("5h", "5h", 0.27), win("7d", "7d", 0.5)}}},
		{Source: sources["codex"], Snapshot: &quota.Snapshot{Windows: []quota.Window{win("7d", "7d", 0.27)}, ResetCredits: quota.Int(1)}},
		{Source: sources["openrouter"], Snapshot: &quota.Snapshot{Credits: &quota.Balance{Remaining: quota.Frac(12.5), Currency: "$"}}},
		{Source: sources["xai"], Snapshot: &quota.Snapshot{NeedsAuth: true}},
	}
}

var cfg = Config{PercentUsed: true, CreditHealthyUSD: 50, CreditCriticalUSD: 5, HideSignedOut: true}

func render(src string, v []quota.View, short bool, inUse ...string) string {
	return strings.Join(Render(plainTheme{}, Parse(src, resolve), v, cfg, 0, short, inUse), " | ")
}

func TestNamedWindowsAndRemaining(t *testing.T) {
	got := render("{claude:id}: {claude:window-5h-used-percent} {claude:window-7d-remaining-percent}", views(), false)
	if got != "claude: 27% 50%" {
		t.Fatalf("got %q", got)
	}
}

func TestConditionalGroupFollowsResetCredits(t *testing.T) {
	src := "{codex:id}: {codex:window-7d-used-percent}[ ({codex:resets-available}r)]"
	if got := render(src, views(), false); got != "codex: 27% (1r)" {
		t.Fatalf("with credit: %q", got)
	}
	v := views()
	v[1].Snapshot.ResetCredits = quota.Int(0)
	if got := render(src, v, false); got != "codex: 27%" {
		t.Fatalf("zero credits must drop the group: %q", got)
	}
}

func TestUnknownIsNotZero(t *testing.T) {
	v := views()
	v[0].Snapshot.Windows[0].Used = nil
	if got := render("{claude:window-5h-used-percent}", v, false); got != "?" {
		t.Fatalf("got %q", got)
	}
	if got := render("{claude:window-9h-used-percent}", v, false); got != "?" {
		t.Fatalf("missing window: %q", got)
	}
}

func TestSignedOutHiddenDropsSegmentAndLoginWhenShown(t *testing.T) {
	src := "{claude:id} · {xai:id}: {xai:pct}"
	if got := render(src, views(), false); got != "claude" {
		t.Fatalf("hidden: %q", got)
	}
	c := cfg
	c.HideSignedOut = false
	got := strings.Join(Render(plainTheme{}, Parse(src, resolve), views(), c, 0, false, nil), "|")
	if got != "claude|xai: login" {
		t.Fatalf("shown: %q", got)
	}
}

func TestAllSkipsExplicitAndInUseGates(t *testing.T) {
	if got := render("{claude:id}{sep}{all:short}", views(), false); got != "claude | cox: 27% (r 1) | or: 12.50$" {
		t.Fatalf("all: %q", got)
	}
	src := "[{codex:in-use,id,pct}]"
	if got := render(src, views(), false); got != "" {
		t.Fatalf("not in use must hide: %q", got)
	}
	if got := render(src, views(), false, "codex"); got != "codex: 27%" {
		t.Fatalf("in use: %q", got)
	}
	if got := render("{in-use}", views(), false, "claude"); !strings.Contains(got, "Claude") || !strings.Contains(got, "+2") {
		t.Fatalf("in-use summary: %q", got)
	}
}

func TestStyleAndMalformedSyntax(t *testing.T) {
	if got := render("{claude:id|fg=#ff0000}", views(), false); got != "<#ff0000>claude</>" {
		t.Fatalf("style: %q", got)
	}
	tpl := Parse("{claude:id|fg=nope} {nobody:id} {claude:wat} ]x [y {z", resolve)
	if len(tpl.Unknown) != 6 {
		t.Fatalf("unknown: %q", tpl.Unknown)
	}
	if got := Parse(`\{literal\}`, resolve); got.Parts[0].Text != "{literal}" {
		t.Fatalf("escape: %+v", got.Parts)
	}
}

func TestPackNeverExceedsWidth(t *testing.T) {
	segs := Render(plainTheme{}, Parse("{all}", resolve), views(), cfg, 0, false, nil)
	for _, w := range []int{1, 12, 36, 57, 80, 140} {
		for _, row := range Pack(plainTheme{}, segs, w) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: %q", w, row)
			}
		}
	}
	rows := Pack(plainTheme{}, []string{"a", "b", "c", "d"}, 3)
	if got := LimitRows(plainTheme{}, rows, 2, 40); len(got) != 2 || !strings.Contains(got[1], "4 rows") {
		t.Fatalf("limit: %q", got)
	}
}

func TestCreditStressBounds(t *testing.T) {
	if CreditStress(0, cfg) != 1 || CreditStress(50, cfg) != 0 {
		t.Fatal("bounds")
	}
	if s := CreditStress(27, cfg); s <= 0 || s >= 0.8 {
		t.Fatalf("mid %v", s)
	}
}

func TestResetCountdown(t *testing.T) {
	ms := func(s int64) *int64 { v := s * 1000; return &v }
	for in, want := range map[int64]string{30: "0m", 3660: "1h 1m", 90000: "1d 1h"} {
		if got := ResetCountdown(ms(in), 0); got != want {
			t.Fatalf("%d: %q want %q", in, got, want)
		}
	}
	if ResetCountdown(nil, 0) != "" || ResetCountdown(ms(-5), 0) != "" {
		t.Fatal("unknown must be empty")
	}
}
