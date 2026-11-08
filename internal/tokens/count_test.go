package tokens

import "testing"

func TestSumTakesTheWeakestSourceAndAnEmptySumIsExact(t *testing.T) {
	got := Sum(Count{Tokens: 10, Source: SourceProvider}, Count{Tokens: 5, Source: SourceEstimate}, Count{Tokens: 2, Source: SourceEndpoint})
	if got.Tokens != 17 || got.Source != SourceEstimate {
		t.Fatalf("%+v", got)
	}
	if z := Sum(); z.Tokens != 0 || z.Source != SourceProvider {
		t.Fatalf("empty sum %+v", z)
	}
}

func TestSumDeduplicatesDiagnostics(t *testing.T) {
	d := Diagnostic{Provider: "claude", Message: "login"}
	got := Sum(Count{Source: SourceEstimate, Diagnostics: []Diagnostic{d}}, Count{Source: SourceEstimate, Diagnostics: []Diagnostic{d}})
	if len(got.Diagnostics) != 1 {
		t.Fatalf("%+v", got.Diagnostics)
	}
}

func TestEstimateRoundsUpAndSeparatesKinds(t *testing.T) {
	r := Ratios{Prose: 4, JSON: 2}
	if c := r.Estimate("abcde", Prose); c.Tokens != 2 || c.Source != SourceEstimate {
		t.Fatalf("%+v", c)
	}
	if c := r.Estimate(`{"a":1}`, KindOf(`{"a":1}`)); c.Tokens != 4 {
		t.Fatalf("json %+v", c)
	}
	if c := r.Estimate("", Prose); c.Tokens != 0 {
		t.Fatalf("empty %+v", c)
	}
}

func TestEstimateMeasuresUTF16UnitsLikeTheHost(t *testing.T) {
	// One astral rune is two UTF-16 units: 4 units at 4 per token is one token, two runes are two.
	if c := (Ratios{Prose: 4, JSON: 4}).Estimate("😀😀", Prose); c.Tokens != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestRatiosForRoutes(t *testing.T) {
	if RatiosFor("antigravity", "claude-sonnet-5-5").JSON != 2.08 || RatiosFor("synthetic", "hf:x/y") != DefaultRatios || RatiosFor("other", "m") != DefaultRatios {
		t.Fatal("route ratios")
	}
}
