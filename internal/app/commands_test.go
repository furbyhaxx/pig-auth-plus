package app

import "testing"

func TestCompletionsWorkBeforeASession(t *testing.T) {
	a := &App{}
	got, err := a.authCompletions("qu")
	if err != nil || len(got) != 1 || got[0].Value != "quotas" {
		t.Fatalf("%v %v", got, err)
	}
	got, _ = a.authCompletions("settings quotas.linePl")
	// The host replaces the whole argument with Value, so it keeps the typed section.
	if len(got) != 1 || got[0].Value != "settings quotas.linePlacement" || got[0].Label != "quotas.linePlacement" {
		t.Fatalf("%v", got)
	}
	got, _ = a.quotaCompletions("")
	if len(got) == 0 || got[0].Value != "reset" {
		t.Fatalf("reset form missing: %v", got)
	}
}
