package synthetic

import "testing"

func TestFoldInterleaved(t *testing.T) {
	content := []any{
		map[string]any{"type": "thinking", "thinking": "a"},
		map[string]any{"type": "text", "text": "x"},
		map[string]any{"type": "thinking", "thinking": "b"},
		map[string]any{"type": "toolCall", "id": "t1"},
		map[string]any{"type": "text", "text": "y"},
	}
	folded, ok := foldInterleaved(content)
	if !ok || len(folded) != 3 {
		t.Fatalf("folded = %v", folded)
	}
	if folded[0].(map[string]any)["thinking"] != "ab" || folded[1].(map[string]any)["text"] != "xy" || folded[2].(map[string]any)["id"] != "t1" {
		t.Fatalf("folded = %v", folded)
	}
	signed := []any{
		map[string]any{"type": "thinking", "thinking": "a", "thinkingSignature": "s"},
		map[string]any{"type": "thinking", "thinking": "b"},
	}
	if _, ok := foldInterleaved(signed); ok {
		t.Fatal("signed reasoning folded")
	}
	if _, ok := foldInterleaved(content[:2]); ok {
		t.Fatal("single blocks folded")
	}
}

func TestCorrectUsageDedupesAndRescales(t *testing.T) {
	usage := map[string]any{
		"input": 400.0, "output": 50.0, "cacheRead": 200.0, "cacheWrite": 400.0, "totalTokens": 1050.0,
		"cost": map[string]any{"input": 1.0, "output": 2.0, "cacheRead": 0.5, "cacheWrite": 3.0, "total": 6.5},
	}
	var gotReported int
	count := func(model string, reported int, _ []byte) (int, bool) {
		gotReported = reported
		return 1500, true
	}
	rates := map[string]any{"input": 1e6 / 400, "output": 1e6 / 50, "cacheRead": 1e6 / 200, "cacheWrite": 5.0}
	out := correctUsage(usage, "hf:a/b", rates, nil, count)
	if gotReported != 600 {
		t.Fatalf("reported prompt = %d, want 600 without the duplicate write", gotReported)
	}
	if out["input"] != 1000 || out["cacheRead"] != 500 || out["cacheWrite"] != 0 || out["output"] != 50.0 || out["totalTokens"] != 1550 {
		t.Fatalf("usage = %v", out)
	}
	cost := out["cost"].(map[string]any)
	if cost["cacheWrite"] != 0.0 || cost["total"] != 3.0 || cost["input"] != 1.0 || cost["output"] != 1.0 {
		t.Fatalf("cost = %v", cost)
	}
	if usage["input"] != 400.0 {
		t.Fatal("input usage mutated")
	}
}

func TestCorrectUsageWithoutCounterKeepsUnits(t *testing.T) {
	usage := map[string]any{"input": 10.0, "output": 5.0, "cacheRead": 0.0, "cacheWrite": 3.0}
	out := correctUsage(usage, "m", nil, nil, func(string, int, []byte) (int, bool) { return 0, false })
	if out["input"] != 10.0 || out["cacheWrite"] != 3.0 || out["totalTokens"] != 18 {
		t.Fatalf("usage = %v", out)
	}
}
