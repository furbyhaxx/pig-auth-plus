package tokens

import (
	"testing"

	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

func TestNoPrintableKeyRecounts(t *testing.T) {
	recounts := 0
	tb := New(func() *tokens.Report { return nil }, func() Display { return Display{} }, func() { recounts++ })
	for c := rune(0x20); c < 0x7f; c++ {
		if tb.Key(string(c)) {
			t.Fatalf("printable %q was taken as an action", c)
		}
	}
	if recounts != 0 {
		t.Fatal("printable keys must not recount")
	}
	if !tb.Key("\x1br") || recounts != 1 {
		t.Fatalf("alt+r must recount once, got %d", recounts)
	}
}
