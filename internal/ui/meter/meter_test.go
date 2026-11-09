package meter

import (
	"strings"
	"testing"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

type plain struct{}

func (plain) Paint(text string, _ line.Style) string { return text }

func TestBarFillsProportionallyAndUnknownIsNotEmpty(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		used *float64
		want string
	}{{f(0), "░░░░"}, {f(0.5), "██░░"}, {f(1), "████"}, {f(7), "████"}, {f(-1), "░░░░"}, {nil, "····"}} {
		if got := Bar(plain{}, c.used, 4); got != c.want {
			t.Errorf("Bar(%v) = %q, want %q", c.used, got, c.want)
		}
	}
	if Bar(plain{}, nil, 0) != "" || strings.Contains(Bar(plain{}, f(0.3), 6), "·") {
		t.Fatal("zero cells draw nothing; a known value never draws unknown dots")
	}
}
