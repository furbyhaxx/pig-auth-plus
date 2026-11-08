package tokens

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMessageSegmentsToolResultJSONIsJSONKind(t *testing.T) {
	got := MessageSegments(decode(t, `{"role":"toolResult","content":[{"type":"text","text":"{\"a\":1}"},{"type":"text","text":"done"}]}`))
	if len(got) != 2 || got[0].Kind != JSON || got[1].Kind != Prose {
		t.Fatalf("%+v", got)
	}
}

func TestMessageSegmentsAssistantBlocks(t *testing.T) {
	got := MessageSegments(decode(t, `{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"toolCall","name":"read","arguments":{"path":"x"}},{"type":"image"}]}`))
	if len(got) != 4 || got[0].Text != "hm" || got[1].Text != "read" || got[2].Text != `{"path":"x"}` || !got[3].Image {
		t.Fatalf("%+v", got)
	}
}

func TestMessageSegmentsHiddenBashAndSummaries(t *testing.T) {
	if MessageSegments(decode(t, `{"role":"bashExecution","command":"ls","excludeFromContext":true}`)) != nil {
		t.Fatal("excluded bash must not count")
	}
	b := MessageSegments(decode(t, `{"role":"bashExecution","command":"ls","output":"a","exitCode":2}`))
	if len(b) != 1 || b[0].Text != "Ran `ls`\n```\na\n```\n\nCommand exited with code 2" {
		t.Fatalf("%q", b[0].Text)
	}
	if s := MessageSegments(decode(t, `{"role":"compactionSummary","summary":"S"}`)); len(s) != 1 {
		t.Fatal("compaction summary")
	}
}

func TestCurrentSystemReplaysSectionsAndTools(t *testing.T) {
	msgs := []map[string]any{
		decode(t, `{"role":"system","content":"base","sections":{"a":"one","b":"two"},"toolsAdded":[{"name":"t1","description":"d","parameters":{}},{"name":"t2"}]}`),
		decode(t, `{"role":"user","content":"hi"}`),
		decode(t, `{"role":"system","sections":{"a":null,"b":"three"},"toolsRemoved":[{"name":"t1"}]}`),
	}
	st, ok := CurrentSystem(msgs)
	if !ok || st.Messages != 2 || len(st.Segments) != 2 || st.Segments[0].Text != "base" || st.Segments[1].Text != "three" {
		t.Fatalf("%+v", st)
	}
	if len(st.Tools) != 1 || st.Tools[0].Name != "t2" {
		t.Fatalf("%+v", st.Tools)
	}
	if _, ok := CurrentSystem(msgs[1:2]); ok {
		t.Fatal("no system message must report false")
	}
}

func TestFindAnchorSkipsAbortedAndErrors(t *testing.T) {
	msgs := []map[string]any{
		decode(t, `{"role":"user","content":"q"}`),
		decode(t, `{"role":"assistant","stopReason":"stop","usage":{"totalTokens":100}}`),
		decode(t, `{"role":"user","content":"q2"}`),
		decode(t, `{"role":"assistant","stopReason":"error","usage":{"totalTokens":999}}`),
		decode(t, `{"role":"assistant","stopReason":"aborted","usage":{"input":5}}`),
	}
	entries := []ProjectedEntry{{"e1", 1}, {"e2", 1}, {"e3", 1}, {"e4", 2}}
	branch := []BranchEntry{{"e1", "message"}, {"e2", "message"}, {"e3", "message"}, {"e4", "message"}}
	a, ok := FindAnchor(msgs, entries, branch)
	if !ok || a.Index != 1 || a.Tokens != 100 {
		t.Fatalf("%+v %v", a, ok)
	}
}

func TestFindAnchorSumsComponentsWithoutTotalAndHonorsInvalidation(t *testing.T) {
	msgs := []map[string]any{decode(t, `{"role":"assistant","stopReason":"stop","usage":{"input":10,"output":5,"cacheRead":3,"cacheWrite":2}}`)}
	entries := []ProjectedEntry{{"e1", 1}}
	if a, ok := FindAnchor(msgs, entries, []BranchEntry{{"e1", "message"}}); !ok || a.Tokens != 20 {
		t.Fatalf("%+v %v", a, ok)
	}
	if _, ok := FindAnchor(msgs, entries, []BranchEntry{{"e1", "message"}, {"c", "compaction"}}); ok {
		t.Fatal("a later compaction must invalidate the anchor")
	}
	if _, ok := FindAnchor(msgs, entries, []BranchEntry{{"x", "message"}}); ok {
		t.Fatal("an entry off the branch must not anchor")
	}
}
