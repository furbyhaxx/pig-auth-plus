package command

import (
	"strings"
	"testing"
)

func resolver(name string) string {
	switch strings.ToLower(name) {
	case "codex", "cx", "openai":
		return "codex"
	case "claude", "cc":
		return "claude"
	}
	return ""
}

var keys = []string{"quotas.linePlacement", "quotas.providers", "claude.cacheRetention"}

func TestAuthSectionsAndAliases(t *testing.T) {
	for in, want := range map[string]Section{
		"": SectionProviders, "status": SectionProviders, "LIMITS": SectionQuotas, "token": SectionTokens, "model": SectionModels, "config": SectionSettings,
	} {
		r, err := Auth(in, resolver, keys)
		if err != nil || r.Section != want {
			t.Errorf("Auth(%q) = %+v, %v; want %s", in, r, err, want)
		}
	}
}

func TestAuthQuotasTargetsAResolvedProvider(t *testing.T) {
	r, err := Auth("quotas CX", resolver, keys)
	if err != nil || r.Provider != "codex" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAuthRejectsUnknownSectionWithNearbyNames(t *testing.T) {
	_, err := Auth("quota-s", resolver, keys)
	if err == nil || !strings.Contains(err.Error(), "Unknown section") || !strings.Contains(err.Error(), "Usage: /auth") {
		t.Fatalf("%v", err)
	}
	_, err = Auth("setings", resolver, keys)
	if err == nil {
		t.Fatal("typo accepted")
	}
	_, err = Auth("tokens extra", resolver, keys)
	if err == nil || !strings.Contains(err.Error(), "no arguments") {
		t.Fatalf("%v", err)
	}
}

func TestSettingsKeyIsMatchedCaseInsensitivelyAndRejectedWhenUnknown(t *testing.T) {
	r, err := Auth("settings QUOTAS.lineplacement", resolver, keys)
	if err != nil || r.Key != "quotas.linePlacement" {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = Auth("settings quotas.linePlacemnt", resolver, keys)
	if err == nil || !strings.Contains(err.Error(), "quotas.linePlacement") {
		t.Fatalf("no suggestion: %v", err)
	}
}

func TestQuotasResetForms(t *testing.T) {
	for in, want := range map[string]Request{
		"reset":        {Section: SectionQuotas, Reset: true},
		"resets codex": {Section: SectionQuotas, Reset: true, Provider: "codex"},
		"RESET openai": {Section: SectionQuotas, Reset: true, Provider: "codex"},
		"claude":       {Section: SectionQuotas, Provider: "claude"},
		"":             {Section: SectionQuotas},
	} {
		r, err := Quotas(in, resolver)
		if err != nil || r != want {
			t.Errorf("Quotas(%q) = %+v, %v; want %+v", in, r, err, want)
		}
	}
}

func TestQuotasRejectsUnknownProviderAndExtraWords(t *testing.T) {
	for _, in := range []string{"reset nobody", "reset codex now", "nobody", "claude codex"} {
		if _, err := Quotas(in, resolver); err == nil {
			t.Errorf("Quotas(%q) accepted", in)
		}
	}
}

func TestUnknownWordIsEchoedWithoutControlCharacters(t *testing.T) {
	_, err := Quotas("\x1b[31mevil", resolver)
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) {
		t.Fatalf("%q", err)
	}
}

func TestTokensTakesNoArguments(t *testing.T) {
	if _, err := Tokens(""); err != nil {
		t.Fatal(err)
	}
	if _, err := Tokens("now"); err == nil {
		t.Fatal("argument accepted")
	}
}
