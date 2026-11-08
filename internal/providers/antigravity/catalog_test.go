package antigravity

import "testing"

func TestBundledCatalogGroupsLevelsPerFamily(t *testing.T) {
	c, err := NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, m := range c.Models() {
		byID[m["id"].(string)] = m
	}
	argon := byID["gemini-4-argon"]
	if argon == nil || argon["name"] != "Gemini 4 Argon" {
		t.Fatalf("argon = %v; ids=%v", argon, keys(byID))
	}
	low, err := PickLevel(c.LevelMap("gemini-4-argon"), "low")
	if err != nil || low.ID != "gemini-4-argon-low" || low.Thinking.Kind != "level" || low.Thinking.Effort != "LOW" {
		t.Fatalf("low = %+v %v", low, err)
	}
	// xhigh is not offered: clamp down to the highest offered level.
	high, _ := PickLevel(c.LevelMap("gemini-4-argon"), "xhigh")
	if high.ID != "gemini-4-argon-high" {
		t.Fatalf("xhigh = %+v", high)
	}
	// No level requested takes the cheapest.
	cheap, _ := PickLevel(c.LevelMap("gemini-4-argon"), "")
	if cheap.ID != "gemini-4-argon-low" {
		t.Fatalf("default = %+v", cheap)
	}
	for id, m := range byID {
		if len(id) > 6 && id[:6] == "claude" {
			l, err := PickLevel(m["thinkingLevelMap"].(map[string]any), "medium")
			if err != nil || l.Thinking.Kind != "adaptive" || l.Thinking.Config()["thinkingBudget"] != 0 {
				t.Fatalf("%s: %+v %v", id, l, err)
			}
		}
	}
}

func TestParseCatalogRejectsEnvelopeAndSkipsBadRows(t *testing.T) {
	if _, err := ParseCatalog([]byte(`{"models":{}}`)); err == nil {
		t.Fatal("missing agentModelSorts accepted")
	}
	models, err := ParseCatalog([]byte(`{"models":{"x-low":{"displayName":"X (Low)","model":"M","maxTokens":10,"maxOutputTokens":5,"thinkingBudget":-1},"y-low":{"model":"M"}},"agentModelSorts":[{"groups":[{"modelIds":["x-low","y-low","z"]}]}]}`))
	if err != nil || len(models) != 1 || models[0]["id"] != "x" || models[0]["name"] != "X" {
		t.Fatalf("models = %v %v", models, err)
	}
}

func TestLevelRoundTrip(t *testing.T) {
	for _, l := range []Level{
		{ID: "a-low", Enum: "E", Thinking: Thinking{Kind: "level", Effort: "LOW"}},
		{ID: "b-high", Enum: "E", Thinking: Thinking{Kind: "budget", Budget: -1}},
	} {
		got, ok := DecodeLevel(l.Encode())
		if !ok || got != l {
			t.Fatalf("%+v -> %+v", l, got)
		}
	}
	for _, bad := range []string{"", "a|b", "a|b|level:HUGE", "a|b|budget:-2", "a|b|x:1"} {
		if _, ok := DecodeLevel(bad); ok {
			t.Fatalf("decoded %q", bad)
		}
	}
}

func keys(m map[string]map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
