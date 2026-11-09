package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

const (
	claudeOAuthToken = "sk-ant-oat-test-token"
	testOrg          = "3f5b8c1e-37a4-4c2d-9e11-8a6f0c4b7d92"
)

var requestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func claudeGrant(over map[string]any) map[string]any {
	g := map[string]any{
		"id": "launch-2610-fable", "label": "Launch grant", "resets_total": 1.0, "resets_left": 1.0,
		"starts_at": "2026-11-09T18:00:00+00:00", "ends_at": "2027-03-09T07:59:00+00:00",
		"clears": []any{"seven_day_overage_included"}, "paused": false, "usable_now": true, "type": "full",
	}
	for k, v := range over {
		g[k] = v
	}
	return g
}

func TestClaudeCedarEmberCountsUsableGrantsAndKeepsWindows(t *testing.T) {
	body := fixture(t, "claude-usage-cedar-ember.json")
	s, err := ParseClaude(body, now)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded account has three grants, each with one reset left, all usable and unpaused.
	if s.ResetCredits == nil || *s.ResetCredits != 3 {
		t.Fatalf("reset credits = %v, want 3", s.ResetCredits)
	}
	without := fixture(t, "claude-usage-cedar-ember.json")
	delete(without, "cedar_ember")
	plain, err := ParseClaude(without, now)
	if err != nil {
		t.Fatal(err)
	}
	if plain.ResetCredits != nil {
		t.Fatalf("an absent grant block must stay unknown: %v", *plain.ResetCredits)
	}
	if !reflect.DeepEqual(s.Windows, plain.Windows) {
		t.Fatalf("grant block changed windows: %+v vs %+v", s.Windows, plain.Windows)
	}

	l, err := ParseClaudeResets(body, now)
	if err != nil || l.Available != 3 || len(l.Credits) != 3 {
		t.Fatalf("list = %+v err=%v", l, err)
	}
	var ids, status []string
	for _, c := range l.Credits {
		ids = append(ids, c.ID)
		status = append(status, c.Status)
	}
	wantIDs := []string{"launch-2610-fable", "launch-2610-fable-12mo", "launch-2610-fable-6mo"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}
	if want := []string{"available", "queued", "queued"}; !reflect.DeepEqual(status, want) {
		t.Fatalf("status = %v, want %v", status, want)
	}
	first := l.Credits[0]
	if first.Type != "full" || first.Description != "clears seven_day_overage_included" || first.Title == "" {
		t.Fatalf("credit = %+v", first)
	}
	expires := time.Date(2027, 3, 9, 7, 59, 0, 0, time.UTC).UnixMilli()
	if first.ExpiresAtMs == nil || *first.ExpiresAtMs != expires || first.GrantedAtMs == nil {
		t.Fatalf("dates = %+v", first)
	}
}

func TestClaudeGrantsExcludeSpentPausedAndUnusableOnes(t *testing.T) {
	l, err := ParseClaudeResets(map[string]any{"cedar_ember": map[string]any{
		"next_grant_id": "launch-2610-fable",
		"grants": []any{
			claudeGrant(map[string]any{"resets_left": 0.0}),
			claudeGrant(map[string]any{"id": "paused", "paused": true}),
			claudeGrant(map[string]any{"id": "not-usable", "usable_now": false}),
		},
	}}, now)
	if err != nil || l.Available != 0 || len(l.Credits) != 2 {
		t.Fatalf("list = %+v err=%v", l, err)
	}
	for _, c := range l.Credits {
		if c.Status != "unavailable" {
			t.Fatalf("credit = %+v", c)
		}
	}
	// A usable grant that the server did not name as next stays queued.
	l, err = ParseClaudeResets(map[string]any{"cedar_ember": map[string]any{
		"grants": []any{claudeGrant(nil)},
	}}, now)
	if err != nil || l.Available != 1 || l.Credits[0].Status != "queued" {
		t.Fatalf("list = %+v err=%v", l, err)
	}
	// Invalid ids are not addressable credits.
	l, err = ParseClaudeResets(map[string]any{"cedar_ember": map[string]any{
		"next_grant_id": "x", "grants": []any{claudeGrant(map[string]any{"id": "Not An Id"})},
	}}, now)
	if err != nil || len(l.Credits) != 0 {
		t.Fatalf("list = %+v err=%v", l, err)
	}
}

func TestClaudeMalformedGrantBlockLeavesCountUnknownWithoutFailingWindows(t *testing.T) {
	body := fixture(t, "claude-usage-cedar-ember.json")
	body["cedar_ember"] = map[string]any{}
	s, err := ParseClaude(body, now)
	if err != nil {
		t.Fatalf("a malformed grant block must not fail the window poll: %v", err)
	}
	if s.ResetCredits != nil || len(s.Windows) == 0 {
		t.Fatalf("snapshot = %+v", s)
	}
	if _, err := ParseClaudeResets(body, now); err == nil {
		t.Fatal("a grant block without grants[] must fail the listing")
	}
	body["cedar_ember"] = "x"
	if s, err := ParseClaude(body, now); err != nil || s.ResetCredits != nil {
		t.Fatalf("a non-object grant block must stay unknown: %+v err=%v", s.ResetCredits, err)
	}
	if _, err := ParseClaudeResets(body, now); err == nil {
		t.Fatal("a non-object grant block must fail the listing")
	}
	empty, err := ParseClaudeResets(map[string]any{"cedar_ember": nil}, now)
	if err != nil || empty.Available != 0 || len(empty.Credits) != 0 {
		t.Fatalf("a null grant block lists nothing: %+v err=%v", empty, err)
	}
}

func claudeResetsAt(base string) claudeResets {
	return claudeResets{usageURL: base + "/usage", profileURL: base + "/profile", orgsURL: base + "/orgs/"}
}

func TestClaudeResetListPollsTheUsageEndpointOnce(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/usage" || r.Header.Get("Authorization") != "Bearer "+claudeOAuthToken {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustJSON(t, fixture(t, "claude-usage-cedar-ember.json")))
	}))
	defer srv.Close()
	l, err := claudeResetsAt(srv.URL).List(context.Background(), env(claudeOAuthToken))
	if err != nil || l.Available != 3 || l.Provider != "claude" || hits.Load() != 1 {
		t.Fatalf("list = %+v err=%v hits=%d", l, err, hits.Load())
	}
	_, err = claudeResetsAt(srv.URL).List(context.Background(), env(""))
	if err == nil || !contains(err.Error(), "/login claude") || hits.Load() != 1 {
		t.Fatalf("a missing credential must fail before any request: err=%v hits=%d", err, hits.Load())
	}
}

func TestClaudeResetConsumePostsExactlyOnceAndMapsTheVerdict(t *testing.T) {
	cases := []struct {
		result  string
		code    quota.ResetCode
		windows int
	}{
		{"reset", quota.ResetDone, 1},
		{"already_used", quota.AlreadyRedeemed, 0},
		{"not_limited", quota.NothingToReset, 0},
		{"cooldown", quota.Refused, 0},
		{"ineligible", quota.Refused, 0},
		{"unavailable", quota.Refused, 0},
		{"future_verdict", quota.Refused, 0},
	}
	for _, tc := range cases {
		t.Run(tc.result, func(t *testing.T) {
			var gets, posts atomic.Int32
			var path, contentType, auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/profile" {
					gets.Add(1)
					_, _ = w.Write([]byte(`{"account":{"uuid":"other-account"},"organization":{"uuid":"` + testOrg + `"}}`))
					return
				}
				posts.Add(1)
				path, contentType, auth = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization")
				raw, _ := io.ReadAll(r.Body)
				var sent map[string]any
				if err := json.Unmarshal(raw, &sent); err != nil {
					t.Errorf("body is not JSON: %v", err)
				}
				if sent["program"] != "cedar_ember" || sent["grant_id"] != "launch-2610-fable" ||
					!requestID.MatchString(fmt.Sprint(sent["request_id"])) {
					t.Errorf("body = %v", sent)
				}
				_, _ = w.Write([]byte(`{"result":"` + tc.result + `","reason":"cooldown","cleared":["seven_day_overage_included"]}`))
			}))
			defer srv.Close()
			o, err := claudeResetsAt(srv.URL).Consume(context.Background(), env(claudeOAuthToken), "launch-2610-fable")
			if err != nil {
				t.Fatal(err)
			}
			if o.Code != tc.code || o.WindowsReset != tc.windows || o.Provider != "claude" || o.CreditID != "launch-2610-fable" {
				t.Fatalf("outcome = %+v", o)
			}
			if o.Reason != "cooldown" {
				t.Fatalf("reason = %q", o.Reason)
			}
			if gets.Load() != 1 || posts.Load() != 1 {
				t.Fatalf("requests = %d GET, %d POST", gets.Load(), posts.Load())
			}
			if path != "/orgs/"+testOrg+"/reset_rate_limits" || contentType != "application/json" || auth != "Bearer "+claudeOAuthToken {
				t.Fatalf("POST path=%q contentType=%q auth=%q", path, contentType, auth)
			}
		})
	}
}

func TestClaudeResetConsumeNeverFollowsARedirect(t *testing.T) {
	var posts, followed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/profile":
			_, _ = w.Write([]byte(`{"organization":{"uuid":"` + testOrg + `"}}`))
		case "/orgs/" + testOrg + "/reset_rate_limits":
			posts.Add(1)
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			followed.Add(1)
			_, _ = w.Write([]byte(`{"result":"reset"}`))
		}
	}))
	defer srv.Close()
	_, err := claudeResetsAt(srv.URL).Consume(context.Background(), env(claudeOAuthToken), "launch-2610-fable")
	if err == nil || followed.Load() != 0 || posts.Load() != 1 {
		t.Fatalf("err=%v posts=%d followed=%d", err, posts.Load(), followed.Load())
	}
	if !contains(err.Error(), "may already be spent") || !contains(err.Error(), "302") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeResetConsumeFailuresNeverLeakIdentityOrRetry(t *testing.T) {
	cases := []struct {
		name  string
		reply func(w http.ResponseWriter, r *http.Request)
		want  string
	}{
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, claudeOAuthToken+" "+testOrg, http.StatusInternalServerError)
		}, "may already be spent"},
		{"rate limited", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "slow down", http.StatusTooManyRequests)
		}, "rate limited"},
		{"unauthorized", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		}, "/login claude"},
		{"forbidden", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusForbidden)
		}, "/login claude"},
		{"invalid JSON", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}, "may already be spent"},
		{"missing verdict", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"cleared":[]}`))
		}, "may already be spent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/profile" {
					_, _ = w.Write([]byte(`{"organization":{"uuid":"` + testOrg + `"}}`))
					return
				}
				posts.Add(1)
				tc.reply(w, r)
			}))
			defer srv.Close()
			_, err := claudeResetsAt(srv.URL).Consume(context.Background(), env(claudeOAuthToken), "launch-2610-fable")
			if err == nil || posts.Load() != 1 {
				t.Fatalf("posts=%d err=%v", posts.Load(), err)
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if contains(err.Error(), claudeOAuthToken) || contains(err.Error(), testOrg) {
				t.Fatalf("error leaks identity: %v", err)
			}
		})
	}
}

func TestClaudeResetConsumeRejectsBeforeTheFirstRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"account":{"uuid":"other-account"}}`))
	}))
	defer srv.Close()
	r := claudeResetsAt(srv.URL)
	for name, tc := range map[string]struct {
		token  string
		credit string
	}{
		"no credential":   {"", "launch-2610-fable"},
		"api key":         {"sk-ant-api03-abc", "launch-2610-fable"},
		"invalid grant":   {claudeOAuthToken, "../launch"},
		"missing profile": {claudeOAuthToken, "launch-2610-fable"},
	} {
		if _, err := r.Consume(context.Background(), env(tc.token), tc.credit); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("only the credential-bearing profile read may leave: hits=%d", hits.Load())
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
