package modelsfile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const commented = `{
  // my providers
  "providers": {
    "claude": {
      "baseUrl": "https://example.invalid", /* keep */
      "modelOverrides": {
        "claude-opus-5": {
          "contextWindow": 425000, // wide
        },
      },
    },
    "other": { "api": "openai-completions" }
  },
  "extra": [1, 2, 3]
}
`

func apply(t *testing.T, in, provider, model string, ops ...Op) string {
	t.Helper()
	out, err := Apply([]byte(in), provider, model, ops)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(out, provider, model); err != nil {
		t.Fatalf("result is not readable: %v\n%s", err, out)
	}
	return string(out)
}

func TestChangedValueKeepsEverythingElseByteForByte(t *testing.T) {
	out := apply(t, commented, "claude", "claude-opus-5", Op{Path: []string{"contextWindow"}, Value: 100000})
	want := strings.Replace(commented, "425000", "100000", 1)
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestAddedFieldKeepsCommentsAndTrailingComma(t *testing.T) {
	out := apply(t, commented, "claude", "claude-opus-5", Op{Path: []string{"name"}, Value: "Opus"})
	for _, keep := range []string{"// my providers", "/* keep */", "// wide", `"extra": [1, 2, 3]`} {
		if !strings.Contains(out, keep) {
			t.Fatalf("lost %q:\n%s", keep, out)
		}
	}
	got, err := Read([]byte(out), "claude", "claude-opus-5")
	if err != nil || got["name"] != "Opus" || got["contextWindow"] != float64(425000) {
		t.Fatalf("%v %v\n%s", got, err, out)
	}
}

func TestMissingPathIsCreatedUnderExistingProvider(t *testing.T) {
	out := apply(t, commented, "other", "m1", Op{Path: []string{"cost", "input"}, Value: 1.5}, Op{Path: []string{"reasoning"}, Value: true})
	got, err := Read([]byte(out), "other", "m1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"cost": map[string]any{"input": 1.5}, "reasoning": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\n%s", got, out)
	}
	if !strings.Contains(out, `"api": "openai-completions"`) {
		t.Fatalf("sibling key lost:\n%s", out)
	}
}

func TestNewProviderAndEmptyFile(t *testing.T) {
	out := apply(t, "", "claude", "m", Op{Path: []string{"maxTokens"}, Value: 9})
	got, _ := Read([]byte(out), "claude", "m")
	if got["maxTokens"] != float64(9) {
		t.Fatalf("%s", out)
	}
	out = apply(t, "{}", "claude", "m", Op{Path: []string{"maxTokens"}, Value: 9})
	if got, _ := Read([]byte(out), "claude", "m"); got["maxTokens"] != float64(9) {
		t.Fatalf("%s", out)
	}
}

func TestRemovingLastFieldPrunesOnlyOverrideObjects(t *testing.T) {
	out := apply(t, commented, "claude", "claude-opus-5", Op{Path: []string{"contextWindow"}, Remove: true})
	if strings.Contains(out, "modelOverrides") || strings.Contains(out, "claude-opus-5") {
		t.Fatalf("empty override objects must go:\n%s", out)
	}
	if !strings.Contains(out, `"baseUrl"`) || !strings.Contains(out, `"other"`) || !strings.Contains(out, "// my providers") {
		t.Fatalf("provider data must stay:\n%s", out)
	}
}

func TestRemovingOneOfSeveralFields(t *testing.T) {
	in := `{"providers":{"p":{"modelOverrides":{"m":{"a":1,"name":"x","b":2}}}}}`
	for _, key := range []string{"a", "name", "b"} {
		out := apply(t, in, "p", "m", Op{Path: []string{key}, Remove: true})
		got, _ := Read([]byte(out), "p", "m")
		if _, ok := got[key]; ok || len(got) != 2 {
			t.Fatalf("remove %s: %v\n%s", key, got, out)
		}
	}
}

func TestRemovingAnAbsentFieldChangesNothing(t *testing.T) {
	if out := apply(t, commented, "claude", "claude-opus-5", Op{Path: []string{"nope"}, Remove: true}); out != commented {
		t.Fatalf("changed:\n%s", out)
	}
	if out := apply(t, commented, "x", "y", Op{Path: []string{"nope"}, Remove: true}); out != commented {
		t.Fatalf("changed:\n%s", out)
	}
}

func TestMalformedFileIsRefusedAndUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	bad := `{"providers": {`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Update(context.Background(), path, "p", "m", []Op{{Path: []string{"name"}, Value: "x"}})
	if err == nil {
		t.Fatal("malformed file must be refused")
	}
	if got, _ := os.ReadFile(path); string(got) != bad {
		t.Fatalf("file was modified: %q", got)
	}
}

func TestShapeConflictIsAnError(t *testing.T) {
	if _, err := Apply([]byte(`{"providers": []}`), "p", "m", []Op{{Path: []string{"name"}, Value: "x"}}); err == nil {
		t.Fatal("a non-object providers value must be refused")
	}
}

func TestStringsContainingCommentMarkersAreNotComments(t *testing.T) {
	in := `{"providers":{"p":{"baseUrl":"https://h/a//b","modelOverrides":{"m":{"name":"a /* b */ c"}}}}}`
	out := apply(t, in, "p", "m", Op{Path: []string{"maxTokens"}, Value: 1})
	if !strings.Contains(out, `"https://h/a//b"`) || !strings.Contains(out, `"a /* b */ c"`) {
		t.Fatalf("string content changed:\n%s", out)
	}
}

func TestConcurrentUpdatesKeepBoth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	done := make(chan error, 2)
	for _, m := range []string{"a", "b"} {
		go func() {
			done <- Update(context.Background(), path, "p", m, []Op{{Path: []string{"maxTokens"}, Value: 1}})
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	for _, m := range []string{"a", "b"} {
		if got, err := Read(data, "p", m); err != nil || got["maxTokens"] != float64(1) {
			t.Fatalf("%s lost: %v %v\n%s", m, got, err, data)
		}
	}
}

func TestEmptyObjectsExpandOverLines(t *testing.T) {
	out := apply(t, "{}\n", "claude", "m", Op{Path: []string{"cost", "input"}, Value: 1})
	want := "{\n  \"providers\": {\n    \"claude\": {\n      \"modelOverrides\": {\n        \"m\": {\n          \"cost\": {\n            \"input\": 1\n          }\n        }\n      }\n    }\n  }\n}\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestReadAllAndKeys(t *testing.T) {
	all, err := ReadAll([]byte(`{"providers":{"p":{"modelOverrides":{"m":{"name":"x","cost":{"input":1,"output":2}}}},"q":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Keys(all["p"]["m"]); strings.Join(got, ",") != "cost.input,cost.output,name" {
		t.Fatalf("%v", got)
	}
	if _, ok := all["q"]; ok {
		t.Fatal("a provider without overrides must not appear")
	}
}
