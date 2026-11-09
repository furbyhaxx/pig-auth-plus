package settings

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
)

func snap(t *testing.T, content string) func() *config.Snapshot {
	t.Helper()
	m := config.NewManager(t.TempDir() + "/auth-plus.toml")
	_ = m.Load()
	return m.Snapshot
}

func selectKey(t *testing.T, tb *Tab, key string) {
	t.Helper()
	tb.scr.L.Select(key)
	if tb.scr.L.Selected() != key {
		t.Fatalf("no setting %s", key)
	}
}

func TestEveryRowFitsAtAllWidths(t *testing.T) {
	tb := New(snap(t, ""), Actions{Preview: func(string, int) ([]string, []string) {
		return []string{"claude: 5h/27%"}, []string{"{bogus:x}"}
	}}, "quotas.lineTemplate")
	tb.SetNotice("saved to user; higher scope overrides: quotas.linePlacement, tokens.errorPercent", true)
	for _, w := range []int{36, 57, 80, 140} {
		for _, row := range tb.Body(frame.Env{Width: w, Rows: 30}) {
			if tui.VisibleWidth(row) > w {
				t.Fatalf("width %d: %q is %d wide", w, row, tui.VisibleWidth(row))
			}
		}
	}
}

func TestStagingChangesNothingUntilSaved(t *testing.T) {
	get := snap(t, "")
	var saved []config.Edit
	tb := New(get, Actions{Save: func(e []config.Edit) { saved = e }}, "")
	selectKey(t, tb, "quotas.refreshOnRequest")
	tb.Key(" ")
	if !tb.Dirty() {
		t.Fatal("a toggle must stage a draft")
	}
	if got := get().Value("quotas.refreshOnRequest"); got != true {
		t.Fatalf("snapshot changed before save: %v", got)
	}
	tb.Key("\x13") // ctrl+s
	if len(saved) != 1 || saved[0].Key != "quotas.refreshOnRequest" || saved[0].Value != true {
		t.Fatalf("save got %+v", saved)
	}
	tb.Saved([]string{"quotas.refreshOnRequest"})
	if tb.Dirty() {
		t.Fatal("saved edits must leave no draft")
	}
}

func TestIntegerEditingClampsAndRejectsText(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.refreshIntervalSec")
	tb.Key("\r")
	tb.Key("\x15") // ctrl+u clears
	for _, c := range "5000" {
		tb.Key(string(c))
	}
	tb.Key("\r")
	if e := tb.Staged(); len(e) != 1 || e[0].Value != int64(3600) {
		t.Fatalf("expected clamp to 3600, got %+v", e)
	}
	if !strings.Contains(tb.notice, "clamped") {
		t.Fatalf("clamping must be reported: %q", tb.notice)
	}
	tb.Key("\r")
	tb.Key("\x15")
	tb.Key("x")
	tb.Key("\r")
	if tb.editing == nil || !strings.Contains(tb.notice, "integer") {
		t.Fatal("non-numeric input must keep the editor open with an error")
	}
}

func TestBackspaceStagesBuiltInDefault(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.linePlacement")
	tb.Key("\x7f")
	e := tb.Staged()
	if len(e) != 1 || !e[0].Reset {
		t.Fatalf("expected a staged reset, got %+v", e)
	}
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 40}), "\n")
	if !strings.Contains(body, "staged reset") {
		t.Fatalf("a staged reset must be labelled:\n%s", body)
	}
}

func TestPlainTextNamesSource(t *testing.T) {
	out := PlainText(snap(t, "")(), 80, "tokens.errorPercent")
	if !strings.Contains(out, "tokens.errorPercent = 90  (default)") {
		t.Fatal(out)
	}
}

func open(t *testing.T, key string) *Tab {
	t.Helper()
	tb := New(snap(t, ""), Actions{
		Known:   func(string) []string { return []string{"claude", "codex", "xai"} },
		Preview: func(src string, w int) ([]string, []string) { return []string{src}, []string{"{nope:id}"} },
	}, key)
	tb.Key("\r")
	if tb.coll == nil {
		t.Fatalf("%s must open a drill-in editor", key)
	}
	view(tb)
	return tb
}

// view renders the tab once, as the host does between keys, so the list knows its rows.
func view(tb *Tab) []string { return tb.Body(frame.Env{Width: 80, Rows: 20}) }

// press sends keys the way the host does: one key, then a render.
func press(tb *Tab, keys ...string) {
	for _, k := range keys {
		tb.Key(k)
		view(tb)
	}
}

func typeText(tb *Tab, s string) {
	for _, c := range s {
		press(tb, string(c))
	}
}

const (
	down      = "\x1b[B"
	shiftUp   = "\x1b[1;2A"
	backspace = "\x7f"
)

func TestProviderListStagesCheckedAndCustomIDs(t *testing.T) {
	tb := open(t, "quotas.providers")
	press(tb, " ")                    // claude
	press(tb, down, down, down, "\r") // "+ Custom ID"
	typeText(tb, "my-proxy")
	press(tb, "\r", "\x13")
	e := tb.Staged()
	got, _ := e[0].Value.([]string)
	if tb.coll != nil || len(e) != 1 || strings.Join(got, ",") != "claude,my-proxy" {
		t.Fatalf("staged %+v, editor open=%v", e, tb.coll != nil)
	}
}

func TestCustomProviderIDIsMarkedAndRemovable(t *testing.T) {
	tb := open(t, "quotas.providers")
	press(tb, down, down, down, "\r")
	typeText(tb, "zai")
	press(tb, "\r")
	if body := strings.Join(view(tb), "\n"); !strings.Contains(body, "⚠ unknown id") {
		t.Fatalf("a custom id must be flagged:\n%s", body)
	}
	press(tb, backspace)
	if body := strings.Join(view(tb), "\n"); strings.Contains(body, "zai") {
		t.Fatalf("backspace must remove a custom id:\n%s", body)
	}
}

func TestEscOnDirtyEditorNeedsConfirmation(t *testing.T) {
	tb := open(t, "claude.cacheRetentionProviders")
	press(tb, " ")
	if !tb.Dirty() {
		t.Fatal("a changed draft must count as unsaved")
	}
	tb.Key("\x1b")
	if tb.coll == nil {
		t.Fatal("the first Esc must not discard a draft")
	}
	if d := tb.Description(frame.Env{Width: 80}); !strings.Contains(d, "Esc again") {
		t.Fatalf("the confirmation must be visible: %q", d)
	}
	tb.Key("\x1b")
	if tb.coll != nil || len(tb.Staged()) != 0 {
		t.Fatal("the second Esc discards and stages nothing")
	}
}

func TestEscInsideATextInputCancelsTheInputNotTheEditor(t *testing.T) {
	tb := open(t, "quotas.providers")
	press(tb, down, down, down, "\r")
	typeText(tb, "abc")
	tb.Key("\x1b")
	if tb.coll == nil || tb.coll.Capturing() {
		t.Fatal("Esc must close only the input")
	}
	if tb.Dirty() {
		t.Fatal("a cancelled input stages nothing")
	}
}

func TestPatternRulesAddEditReorderDrop(t *testing.T) {
	tb := open(t, "claude.replacePatterns")
	press(tb, "\r") // "+ Rewrite rule"
	typeText(tb, "old")
	press(tb, "\r")
	typeText(tb, "new")
	press(tb, "\r")
	press(tb, down, down, "\r") // "+ Drop rule"
	typeText(tb, "secret")
	press(tb, "\r", shiftUp)
	body := strings.Join(view(tb), "\n")
	if !strings.Contains(body, `1 drop`) || !strings.Contains(body, `"secret"`) || !strings.Contains(body, `2 rewrite`) || !strings.Contains(body, `"old" → "new"`) {
		t.Fatalf("rules not ordered as edited:\n%s", body)
	}
	press(tb, "\x13")
	rules, _ := tb.Staged()[0].Value.([]config.ReplacePattern)
	if len(rules) != 2 || rules[0].HasReplacement || !rules[1].HasReplacement || rules[1].Replacement != "new" {
		t.Fatalf("got %+v", rules)
	}
}

func TestRewriteRuleWithoutAReplacementIsNotAdded(t *testing.T) {
	tb := open(t, "claude.replacePatterns")
	press(tb, "\r")
	typeText(tb, "old")
	press(tb, "\r", "\r") // empty replacement on a rewrite
	if !tb.coll.Capturing() || len(tb.coll.Value().([]config.ReplacePattern)) != 0 {
		t.Fatal("a rewrite needs a replacement; use a drop rule to remove paragraphs")
	}
}

func TestTemplateEditorStagesOnEnterAndPreviewsEveryWidth(t *testing.T) {
	tb := open(t, "quotas.lineTemplate")
	body := strings.Join(tb.Body(frame.Env{Width: 80, Rows: 60}), "\n")
	for _, w := range []string{"140", "80", "57", "36"} {
		if !strings.Contains(body, "Preview at "+w+" cols") {
			t.Fatalf("missing the %s-column preview:\n%s", w, body)
		}
	}
	if !strings.Contains(body, "⚠ ignored: {nope:id}") {
		t.Fatalf("ignored tokens must be listed:\n%s", body)
	}
	if got := tb.Status(frame.Env{}); !strings.Contains(got, "1 ignored token") {
		t.Fatalf("status %q", got)
	}
	typeText(tb, "!")
	press(tb, "\r")
	e := tb.Staged()
	if tb.coll != nil || len(e) != 1 || !strings.HasSuffix(e[0].Value.(string), "!") {
		t.Fatalf("enter must stage the edited template: %+v", e)
	}
}

func TestBlankTemplateCannotBeStaged(t *testing.T) {
	tb := open(t, "quotas.lineTemplate")
	press(tb, "\x15") // ctrl+u clears
	press(tb, "\r")
	if tb.coll == nil {
		t.Fatal("a blank template must keep the editor open")
	}
}

func TestDrillInEditorsFitAllWidthsAndReportOverflow(t *testing.T) {
	for _, key := range []string{"quotas.providers", "claude.replacePatterns", "quotas.lineTemplate"} {
		tb := open(t, key)
		for _, w := range []int{36, 57, 80, 140} {
			rows := tb.Body(frame.Env{Width: w, Rows: 4})
			if len(rows) > 4 {
				t.Fatalf("%s width %d: %d rows in a 4-row body", key, w, len(rows))
			}
			for _, row := range rows {
				if tui.VisibleWidth(row) > w {
					t.Fatalf("%s width %d: %q", key, w, row)
				}
			}
		}
	}
	tb := open(t, "quotas.lineTemplate")
	tb.Body(frame.Env{Width: 80, Rows: 4})
	if _, below := tb.Overflow(); below == 0 {
		t.Fatal("a template taller than the body must report rows below")
	}
}

func TestDrillInHasBreadcrumbAndStatus(t *testing.T) {
	tb := open(t, "quotas.providers")
	if got := tb.Crumbs(); len(got) != 1 || got[0] != "Providers" || tb.Filterable() {
		t.Fatalf("crumbs %v filterable %v", got, tb.Filterable())
	}
	if got := tb.Status(frame.Env{}); got != "0 selected" {
		t.Fatalf("status %q", got)
	}
	press(tb, " ")
	if got := tb.Status(frame.Env{}); got != "1 selected · ● staged" {
		t.Fatalf("status %q", got)
	}
}

func TestSettingsAreGroupedUnderSectionHeadersWithSourceAndUnits(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	body := strings.Join(tb.Body(frame.Env{Width: 100, Rows: 60}), "\n")
	for _, want := range []string{"Quotas", "Claude", "Refresh interval", "60 s", "[x] on", "‹ below-editor-first ›", "all sources", "default"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
	if tb.scr.L.Selected() == "" || strings.HasPrefix(tb.scr.L.Selected(), "§") {
		t.Fatalf("the cursor must start on a setting, not a header: %q", tb.scr.L.Selected())
	}
}

func TestFilterMatchesTheKeyAndHidesHeaders(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	tb.SetFilter("cacheRetention")
	body := strings.Join(tb.Body(frame.Env{Width: 100, Rows: 20}), "\n")
	if !strings.Contains(body, "Cache retention") || strings.Contains(body, "Refresh interval") || strings.Contains(body, "Quotas") {
		t.Fatalf("filter by key:\n%s", body)
	}
	tb.SetFilter("zzzzzz")
	if body = strings.Join(tb.Body(frame.Env{Width: 100, Rows: 20}), "\n"); !strings.Contains(body, "no setting matches") {
		t.Fatalf("empty result:\n%s", body)
	}
}

func TestSpaceAndBackspaceBelongToTheFilterOnceItHasText(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.refreshOnRequest")
	tb.SetFilter("refresh")
	selectKey(t, tb, "quotas.refreshOnRequest")
	if tb.Key(" ") || tb.Key("\x7f") {
		t.Fatal("with filter text, space and backspace must reach the filter")
	}
	if tb.Dirty() {
		t.Fatal("nothing may be staged by keys that belong to the filter")
	}
	tb.SetFilter("")
	selectKey(t, tb, "quotas.refreshOnRequest")
	if !tb.Key(" ") || !tb.Dirty() {
		t.Fatal("with an empty filter, space toggles")
	}
}

func TestLeftRightSwitchTabsOnRowsThatDoNotStepInPlace(t *testing.T) {
	tb := New(snap(t, ""), Actions{}, "")
	selectKey(t, tb, "quotas.lineTemplate")
	if tb.Key("\x1b[C") || tb.Key("\x1b[D") {
		t.Fatal("arrows on a text setting belong to the frame's tab switch")
	}
	selectKey(t, tb, "quotas.compactWidthCols")
	if !tb.Key("\x1b[C") || !tb.Dirty() {
		t.Fatal("arrows nudge a number")
	}
}

func TestAliasesRowJumpsToTheModelsTab(t *testing.T) {
	var to string
	tb := New(snap(t, ""), Actions{Goto: func(tab string) { to = tab }}, "")
	selectKey(t, tb, "models.aliases")
	tb.Key("\r")
	if to != "models" {
		t.Fatalf("jumped to %q", to)
	}
}

func TestLongTemplateShowsItsTailAndLeavesRoomForPreviews(t *testing.T) {
	tb := open(t, "quotas.lineTemplate")
	body := strings.Join(tb.Body(frame.Env{Width: 36, Rows: 12}), "\n")
	if !strings.Contains(body, "earlier lines") || !strings.Contains(body, "Preview at 140 cols") {
		t.Fatalf("the template must not push the previews out of a 12-row body:\n%s", body)
	}
	if !strings.Contains(body, "▏") {
		t.Fatalf("the cursor end of the template must stay visible:\n%s", body)
	}
}
