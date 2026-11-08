package quota

import "testing"

func TestEffectiveOverlaysNewerHeadersWithoutErasingKnownValues(t *testing.T) {
	endpoint := &Snapshot{Provider: "claude", FetchedAt: 100, Source: FromEndpoint, Tier: "max", Windows: []Window{
		{ID: "5h", Used: Frac(0.2), Value: "20%", ResetAtMs: Ms(1000)},
		{ID: "7d", Used: Frac(0.5)},
	}}
	headers := &HeaderObservation{ObservedAt: 200, Snapshot: Snapshot{Source: FromHeaders, Windows: []Window{
		{ID: "5h", Used: Frac(0.3), Value: "30%"},
		{ID: "7d"}, // unknown usage: must not erase 0.5
		{ID: "7d-opus", Used: Frac(0.1)},
	}}}
	got := Effective(endpoint, headers)
	if got.FetchedAt != 100 || got.Tier != "max" {
		t.Fatalf("endpoint freshness or tier changed: %+v", got)
	}
	w := map[string]Window{}
	for _, x := range got.Windows {
		w[x.ID] = x
	}
	if *w["5h"].Used != 0.3 || *w["5h"].ResetAtMs != 1000 {
		t.Fatalf("5h = %+v", w["5h"])
	}
	if *w["7d"].Used != 0.5 {
		t.Fatalf("unknown header value erased a known one: %+v", w["7d"])
	}
	if w["7d-opus"].Used == nil || len(got.Windows) != 3 {
		t.Fatalf("new window missing: %+v", got.Windows)
	}
	if *endpoint.Windows[0].Used != 0.2 {
		t.Fatal("endpoint snapshot was mutated")
	}
}

func TestEffectiveIgnoresOlderHeaders(t *testing.T) {
	endpoint := &Snapshot{FetchedAt: 300, Windows: []Window{{ID: "5h", Used: Frac(0.2)}}}
	got := Effective(endpoint, &HeaderObservation{ObservedAt: 300, Snapshot: Snapshot{Windows: []Window{{ID: "5h", Used: Frac(0.9)}}}})
	if got != endpoint {
		t.Fatal("headers not newer than the endpoint must be ignored")
	}
}
