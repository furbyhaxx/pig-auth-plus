package antigravity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGeminiSchemaAllowlistConstAndExclusiveBounds(t *testing.T) {
	in := decode(t, `{"type":"object","additionalProperties":false,"$schema":"x","properties":{
		"mode":{"const":"fast"},
		"n":{"type":"integer","exclusiveMinimum":0,"maximum":10,"multipleOf":2},
		"tags":{"type":"array"},
		"kind":{"enum":[1,2]}
	},"required":["mode","missing"]}`)
	got := GeminiSchema(in, false)
	want := decode(t, `{"type":"OBJECT","properties":{
		"mode":{"enum":["fast"]},
		"n":{"type":"INTEGER","minimum":1,"maximum":10,"description":"multipleOf: 2"},
		"tags":{"type":"ARRAY","items":{"type":"STRING"}},
		"kind":{"description":"Allowed values: 1, 2."}
	},"required":["mode"]}`)
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		t.Fatalf("got %s", g)
	}
}

func TestGeminiSchemaMovesNumericConstraintsForTheGPTBridge(t *testing.T) {
	got := GeminiSchema(decode(t, `{"type":"integer","minimum":1,"description":"Count"}`), true).(map[string]any)
	if _, has := got["minimum"]; has || got["description"] != "Count minimum: 1" {
		t.Fatalf("got %v", got)
	}
}

func TestGeminiSchemaMergesObjectAllOfAndMapsOneOf(t *testing.T) {
	got := GeminiSchema(decode(t, `{"allOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"properties":{"b":{"oneOf":[{"type":"string"},{"type":"number"}]}}}]}`), false).(map[string]any)
	props := got["properties"].(map[string]any)
	if got["type"] != "OBJECT" || props["a"] == nil || props["b"].(map[string]any)["anyOf"] == nil {
		t.Fatalf("got %v", got)
	}
}
