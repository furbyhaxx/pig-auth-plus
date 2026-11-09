package providers

import (
	"strings"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

func newTab(jumped *string) *Tab {
	now := time.Now()
	rows := sample(now)
	return New(func() []Row { return rows }, func(id string) Extra {
		if id == "claude" {
			return Extra{Plan: "Max 20x", Headline: "5h 6%", Fields: []Field{{Label: "Quota", Value: "5h 6%", Jump: "quotas"}}}
		}
		return Extra{}
	}, Actions{Goto: func(tab string) { *jumped = tab }})
}

var env = frame.Env{Width: 100, Rows: 10}

func TestEnterOpensDetailEscGoesBackAndKeepsTheRow(t *testing.T) {
	var jumped string
	tab := newTab(&jumped)
	tab.Body(env)
	tab.Key("\x1b[B") // down: synthetic
	tab.Body(env)
	if !tab.Key("\r") {
		t.Fatal("enter must be consumed")
	}
	if got := tab.Crumbs(); len(got) != 1 || got[0] != "Synthetic" {
		t.Fatalf("crumbs %v", got)
	}
	if tab.Filterable() {
		t.Fatal("a detail screen has nothing to filter")
	}
	if !tab.Key("\x1b") {
		t.Fatal("esc in a detail must be taken by the tab, otherwise the dashboard closes")
	}
	tab.Body(env)
	if tab.Crumbs() != nil || tab.list.Selected() != "synthetic" {
		t.Fatalf("back must return to the same row: %v %q", tab.Crumbs(), tab.list.Selected())
	}
	if tab.Key("\x1b") {
		t.Fatal("esc on the list belongs to the frame")
	}
}

func TestDetailJumpOpensTheOwningTab(t *testing.T) {
	var jumped string
	tab := newTab(&jumped)
	tab.Body(env)
	tab.Key("\r")
	for range 4 { // Provider, Credential, Plan, Sign in/out, Quota
		tab.Body(env)
		tab.Key("\x1b[B")
	}
	tab.Body(env)
	tab.Key("\r")
	if jumped != "quotas" {
		t.Fatalf("jumped to %q", jumped)
	}
}

func TestFilterNarrowsTheListAndReportsNoMatch(t *testing.T) {
	var jumped string
	tab := newTab(&jumped)
	tab.SetFilter("anti")
	body := tab.Body(env)
	if len(body) != 1 || !strings.Contains(body[0], "Antigravity") {
		t.Fatalf("body %q", body)
	}
	tab.SetFilter("zzzz")
	if body = tab.Body(env); len(body) != 1 || !strings.Contains(body[0], "no provider matches") {
		t.Fatalf("body %q", body)
	}
}

func TestSecondClickOpensDetail(t *testing.T) {
	var jumped string
	tab := newTab(&jumped)
	tab.Body(env)
	tab.Click(1, 1)
	tab.Body(env)
	if tab.Crumbs() != nil {
		t.Fatal("one click only selects")
	}
	tab.Click(1, 1)
	if got := tab.Crumbs(); len(got) != 1 {
		t.Fatalf("second click must open: %v", got)
	}
}
