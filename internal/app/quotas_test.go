package app

import "testing"

func TestAuthFromAcceptsAPIKeyOrBearerHeader(t *testing.T) {
	if a := authFrom(map[string]any{"auth": map[string]any{"apiKey": "k"}}); a == nil || a.Token != "k" {
		t.Fatalf("api key: %+v", a)
	}
	a := authFrom(map[string]any{"auth": map[string]any{"headers": map[string]any{"authorization": "Bearer tok "}}})
	if a == nil || a.Token != "tok" {
		t.Fatalf("bearer header: %+v", a)
	}
	for name, in := range map[string]map[string]any{
		"empty":     {},
		"basic":     {"auth": map[string]any{"headers": map[string]any{"Authorization": "Basic abc"}}},
		"blank key": {"auth": map[string]any{"apiKey": ""}},
	} {
		if a := authFrom(in); a != nil {
			t.Fatalf("%s must be signed out, got %+v", name, a)
		}
	}
}
