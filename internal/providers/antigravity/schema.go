package antigravity

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// schemaAllowed is Cloud Code's protobuf Schema field allowlist.
var schemaAllowed = map[string]bool{
	"type": true, "properties": true, "items": true, "required": true, "description": true,
	"pattern": true, "title": true, "nullable": true, "propertyOrdering": true, "example": true,
	"default": true, "enum": true, "anyOf": true, "minimum": true, "maximum": true,
	"minLength": true, "maxLength": true, "minItems": true, "maxItems": true,
	"minProperties": true, "maxProperties": true,
}

var schemaNumeric = map[string]bool{
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"multipleOf": true, "minLength": true, "maxLength": true, "minItems": true,
	"maxItems": true, "minProperties": true, "maxProperties": true,
}

// GeminiSchema reduces a JSON Schema to Cloud Code's Schema without renaming properties.
// Dropped constraints become description hints. The GPT bridge (moveNumeric) cannot carry
// numeric constraints at all, so they move to the description too.
func GeminiSchema(schema any, moveNumeric bool) any {
	in, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	var hints []string
	in = intersection(in, &hints)
	out := map[string]any{}
	typ := ""
	if s, ok := in["type"].(string); ok {
		typ = strings.ToUpper(s)
	}
	for _, key := range sortedKeys(in) {
		value := in[key]
		if typ != "" && key == "items" && typ != "ARRAY" {
			continue
		}
		if typ != "" && typ != "OBJECT" && (key == "properties" || key == "required" || key == "propertyOrdering") {
			continue
		}
		if key == "const" {
			if s, ok := value.(string); ok {
				out["enum"] = []any{s}
			} else {
				hints = append(hints, "Must be "+jsonText(value)+".")
			}
			continue
		}
		if schemaNumeric[key] {
			exclusive := key == "exclusiveMinimum" || key == "exclusiveMaximum"
			if n, ok := number(value); ok && exclusive && !moveNumeric && typ == "INTEGER" {
				lower := key == "exclusiveMinimum"
				target, bound := "maximum", math.Ceil(n)-1
				if lower {
					target, bound = "minimum", math.Floor(n)+1
				}
				if math.Abs(bound) <= 1<<53 {
					if existing, ok := number(in[target]); ok {
						bound = map[bool]float64{true: math.Max(existing, bound), false: math.Min(existing, bound)}[lower]
					}
					out[target] = bound
					continue
				}
			}
			if moveNumeric || exclusive || key == "multipleOf" {
				switch value.(type) {
				case string, bool, float64, json.Number:
					hints = append(hints, fmt.Sprintf("%s: %v", key, value))
				}
				continue
			}
		}
		if key == "oneOf" {
			if branches, ok := value.([]any); ok {
				if _, has := in["anyOf"].([]any); !has {
					out["anyOf"] = mapSchemas(branches, moveNumeric)
				} else {
					hints = append(hints, "oneOf: additional union constraints omitted.")
				}
				continue
			}
		}
		if !schemaAllowed[key] {
			continue
		}
		switch key {
		case "type":
			if typ != "" {
				out[key] = typ
			} else {
				out[key] = value
			}
		case "properties":
			if props, ok := value.(map[string]any); ok {
				next := make(map[string]any, len(props))
				for name, p := range props {
					next[name] = GeminiSchema(p, moveNumeric)
				}
				out[key] = next
			} else {
				out[key] = value
			}
		case "items":
			out[key] = GeminiSchema(value, moveNumeric)
		case "anyOf":
			if branches, ok := value.([]any); ok {
				out[key] = mapSchemas(branches, moveNumeric)
			} else {
				out[key] = value
			}
		case "enum":
			values, ok := value.([]any)
			if !ok {
				out[key] = value
				break
			}
			if allStrings(values) {
				if _, isConst := in["const"].(string); !isConst {
					out[key] = values
				}
			} else {
				parts := make([]string, len(values))
				for i, v := range values {
					parts[i] = jsonText(v)
				}
				hints = append(hints, "Allowed values: "+strings.Join(parts, ", ")+".")
			}
		case "required":
			names, ok := value.([]any)
			if !ok {
				out[key] = value
				break
			}
			props, hasProps := in["properties"].(map[string]any)
			var kept []any
			for _, n := range names {
				s, ok := n.(string)
				if !ok {
					continue
				}
				if _, exists := props[s]; !hasProps || exists {
					kept = append(kept, s)
				}
			}
			if len(kept) > 0 {
				out[key] = kept
			}
		case "minimum", "maximum":
			prev, hadPrev := number(out[key])
			n, isNum := number(value)
			if hadPrev && isNum {
				if key == "minimum" {
					out[key] = math.Max(prev, n)
				} else {
					out[key] = math.Min(prev, n)
				}
			} else {
				out[key] = value
			}
		default:
			out[key] = value
		}
	}
	if len(hints) > 0 {
		desc, _ := out["description"].(string)
		out["description"] = strings.TrimSpace(desc + " " + strings.Join(hints, ", "))
	}
	if out["type"] == "ARRAY" && out["items"] == nil {
		out["items"] = map[string]any{"type": "STRING"}
	}
	return out
}

// intersection merges an allOf of object schemas; anything else keeps the first branch.
func intersection(schema map[string]any, hints *[]string) map[string]any {
	branches, ok := schema["allOf"].([]any)
	if !ok {
		return schema
	}
	base := make(map[string]any, len(schema))
	for k, v := range schema {
		if k != "allOf" {
			base[k] = v
		}
	}
	first := func(reason string) map[string]any {
		*hints = append(*hints, reason)
		out := make(map[string]any, len(base))
		for k, v := range base {
			out[k] = v
		}
		if len(branches) > 0 {
			if b, ok := branches[0].(map[string]any); ok {
				for k, v := range b {
					out[k] = v
				}
			}
		}
		return out
	}
	properties := map[string]any{}
	var required, descriptions []string
	metadata := map[string]any{}
	for _, raw := range append([]any{base}, branches...) {
		branch, ok := raw.(map[string]any)
		if !ok {
			return first("allOf: using the first branch; other intersection constraints omitted.")
		}
		if t, ok := branch["type"].(string); ok && !strings.EqualFold(t, "object") {
			return first("allOf: using the first branch; other intersection constraints omitted.")
		}
		if props, ok := branch["properties"].(map[string]any); ok {
			for name, value := range props {
				if prev, exists := properties[name]; exists && !reflect.DeepEqual(prev, value) {
					return first("allOf: using the first branch; conflicting constraints omitted.")
				}
				properties[name] = value
			}
		}
		if req, ok := branch["required"].([]any); ok {
			for _, n := range req {
				if s, ok := n.(string); ok && !slices.Contains(required, s) {
					required = append(required, s)
				}
			}
		}
		if d, ok := branch["description"].(string); ok {
			descriptions = append(descriptions, d)
		}
		for k, v := range branch {
			if k == "properties" || k == "required" || k == "description" {
				continue
			}
			if _, set := metadata[k]; !set {
				metadata[k] = v
			}
		}
	}
	metadata["type"] = "object"
	metadata["properties"] = properties
	req := make([]any, len(required))
	for i, s := range required {
		req[i] = s
	}
	metadata["required"] = req
	metadata["description"] = strings.Join(descriptions, " ")
	return metadata
}

func mapSchemas(in []any, moveNumeric bool) []any {
	out := make([]any, len(in))
	for i, b := range in {
		out[i] = GeminiSchema(b, moveNumeric)
	}
	return out
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsInf(n, 0) && !math.IsNaN(n)
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func allStrings(values []any) bool {
	for _, v := range values {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
