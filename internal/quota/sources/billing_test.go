package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

func TestCodexUsageFixture(t *testing.T) {
	s, err := ParseCodex(fixture(t, "openai-usage.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	w := byID(s, "primary")
	if s.Tier != "pro" || w == nil || w.Period != "7d" || used(t, w) != 0.27 || !w.Important {
		t.Fatalf("snapshot = %+v", s)
	}
	if w.ResetAtMs == nil || *w.ResetAtMs != now.Add(time.Hour).UnixMilli() {
		t.Fatalf("reset = %v", w.ResetAtMs)
	}
	if s.ResetCredits == nil || *s.ResetCredits != 1 {
		t.Fatalf("credits = %v", s.ResetCredits)
	}
}

func TestCodexUsageWithoutNumbersIsAnError(t *testing.T) {
	if _, err := ParseCodex(map[string]any{"rate_limit": map[string]any{"primary_window": map[string]any{}}}, now); err == nil {
		t.Fatal("a payload without used_percent and credit count must fail, not report 0%")
	}
}

func TestCodexHeadersSkipZeroPlaceholders(t *testing.T) {
	h := map[string]string{
		"x-codex-primary-used-percent": "0", "x-codex-primary-window-minutes": "0", "x-codex-primary-reset-at": "",
		"x-codex-secondary-used-percent": "41", "x-codex-secondary-window-minutes": "10080", "x-codex-plan-type": "plus",
	}
	s := codexHeaders(h, now)
	if s == nil || len(s.Windows) != 1 || s.Windows[0].ID != "secondary" || used(t, &s.Windows[0]) != 0.41 || !s.Windows[0].Important || s.Tier != "plus" {
		t.Fatalf("snapshot = %+v", s)
	}
	if codexHeaders(map[string]string{"x-other": "1"}, now) != nil {
		t.Fatal("unrelated headers claimed")
	}
}

func TestCodexResetCredits(t *testing.T) {
	l, err := ParseCodexResets(fixture(t, "openai-reset-credits.json"), now)
	if err != nil || l.Available < 1 || len(l.Credits) == 0 || l.Credits[0].Status != "available" || l.Credits[0].ExpiresAtMs == nil {
		t.Fatalf("list = %+v err=%v", l, err)
	}
	if _, err := ParseCodexResets(map[string]any{}, now); err == nil {
		t.Fatal("a body without credits[] must fail")
	}
}

func TestCodexOutcomeNeverTreatsUnknownAsSuccess(t *testing.T) {
	if _, err := ParseCodexOutcome(map[string]any{"code": "teapot"}, "c"); err == nil {
		t.Fatal("unrecognized verdict accepted")
	}
	o, err := ParseCodexOutcome(map[string]any{"code": "reset", "windows_reset": 2.0}, "c")
	if err != nil || o.Code != quota.ResetDone || o.WindowsReset != 2 {
		t.Fatalf("outcome = %+v err=%v", o, err)
	}
}

func TestCodexResetFailureWording(t *testing.T) {
	err := resetFailure(statusErr(502), "POST", CodexResetURL+"/consume", true)
	if err == nil || !contains(err.Error(), "may already be spent") {
		t.Fatalf("err = %v", err)
	}
	if err := resetFailure(statusErr(401), "GET", CodexResetURL, false); !contains(err.Error(), "/login openai-codex") {
		t.Fatalf("auth failure hint = %v", err)
	}
}

func TestCodexAuthPrefersPiLoginThenFile(t *testing.T) {
	file := func(time.Time) *quota.Auth { return &quota.Auth{Token: "from-file", AccountID: "acc"} }
	src := Codex(file)
	lookup := func(has bool) func(string) (*quota.Auth, error) {
		return func(p string) (*quota.Auth, error) {
			if has && p == CodexPiProvider {
				return &quota.Auth{Token: "from-pi"}, nil
			}
			return nil, nil
		}
	}
	a, _ := src.AuthFrom(context.Background(), lookup(true))
	if a == nil || a.Token != "from-pi" {
		t.Fatalf("pi login must win: %+v", a)
	}
	a, _ = src.AuthFrom(context.Background(), lookup(false))
	if a == nil || a.Token != "from-file" {
		t.Fatalf("file fallback: %+v", a)
	}
	a, _ = Codex(nil).AuthFrom(context.Background(), lookup(false))
	if a != nil {
		t.Fatalf("no credential must stay nil: %+v", a)
	}
}

func TestCodexRejectsAPIKeysWithoutRequest(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	defer srv.Close()
	env := quota.Env{Now: func() time.Time { return now }, Auth: func(context.Context) (*quota.Auth, error) { return &quota.Auth{Token: "sk-abc"}, nil }}
	s, err := fetchCodex(srv.URL)(context.Background(), env)
	if err != nil || !s.NeedsAuth || n.Load() != 0 {
		t.Fatalf("snapshot=%+v err=%v requests=%d", s, err, n.Load())
	}
}

func TestXAIWeeklyUnusedPeriodIsZeroButEndedPeriodIsUnknown(t *testing.T) {
	credits := map[string]any{"config": map[string]any{"currentPeriod": map[string]any{"end": "2026-10-28T00:00:00Z", "type": "WEEKLY"}, "isUnifiedBillingUser": true}}
	s := ParseXAI(credits, nil, now)
	w := byID(s, "7d")
	if w == nil || used(t, w) != 0 || s.Tier != "unified" {
		t.Fatalf("active period without percent must read 0: %+v", s)
	}
	late := ParseXAI(credits, nil, now.Add(72*time.Hour))
	if w := byID(late, "7d"); w == nil || w.Used != nil {
		t.Fatalf("ended period without percent must be unknown: %+v", late.Windows)
	}
}

func TestXAIFixtureOmitsZeroCaps(t *testing.T) {
	s := ParseXAI(fixture(t, "xai-credits.json"), fixture(t, "xai-monthly.json"), now)
	if len(s.Windows) != 1 || used(t, &s.Windows[0]) != 0.12 {
		t.Fatalf("windows = %+v", s.Windows)
	}
}

func TestXAICaps(t *testing.T) {
	monthly := map[string]any{"config": map[string]any{"monthlyLimit": map[string]any{"val": 20.0}, "used": map[string]any{"val": 5.0}, "billingPeriodEnd": "2026-11-01T00:00:00Z"}}
	credits := map[string]any{"config": map[string]any{"onDemandCap": map[string]any{"val": 10.0}, "onDemandUsed": map[string]any{"val": 2.5}}}
	s := ParseXAI(credits, monthly, now)
	od, mo := byID(s, "ondemand"), byID(s, "monthly")
	if od == nil || od.Value != "$2.50 / $10.00" || used(t, od) != 0.25 || mo == nil || used(t, mo) != 0.25 {
		t.Fatalf("windows = %+v", s.Windows)
	}
}

func TestOpenRouterFixture(t *testing.T) {
	s, err := ParseOpenRouter(fixture(t, "openrouter-credits.json"), fixture(t, "openrouter-key.json"), "c", "k", now)
	if err != nil || s.Credits == nil || s.Credits.Remaining == nil || s.Credits.Total == nil || s.Credits.Currency != "$" {
		t.Fatalf("snapshot = %+v err=%v", s, err)
	}
	want := *s.Credits.Total - *s.Credits.Used
	if want < 0 {
		want = 0
	}
	if *s.Credits.Remaining != want {
		t.Fatalf("remaining = %v want %v", *s.Credits.Remaining, want)
	}
}

func TestOpenRouterKeyRemainingIsFallbackAndEmptyIsError(t *testing.T) {
	key := map[string]any{"data": map[string]any{"limit": 10.0, "limit_remaining": 4.0, "usage_daily": 1.5}}
	s, err := ParseOpenRouter(map[string]any{}, key, "c", "k", now)
	if err != nil || s.Credits.Remaining == nil || *s.Credits.Remaining != 4 || s.Credits.Total != nil || s.Credits.Detail != "day $1.50" {
		t.Fatalf("snapshot = %+v err=%v", s.Credits, err)
	}
	if w := byID(s, "key-cap"); w == nil || used(t, w) != 0.6 {
		t.Fatalf("key cap = %+v", w)
	}
	if _, err := ParseOpenRouter(map[string]any{}, map[string]any{}, "c", "k", now); err == nil {
		t.Fatal("no numbers must be an error, not a zero balance")
	}
}

func TestDeepSeek(t *testing.T) {
	s, err := ParseDeepSeek(fixture(t, "deepseek-balance.json"), "u", now)
	if err != nil || s.Credits == nil || *s.Credits.Remaining != 13.2 || s.Credits.Detail != "granted $5.00 · topped-up $8.20" {
		t.Fatalf("snapshot = %+v err=%v", s.Credits, err)
	}
	cny := map[string]any{"balance_infos": []any{map[string]any{"currency": "CNY", "total_balance": "7"}, map[string]any{"currency": "EUR", "total_balance": "1"}}}
	s, err = ParseDeepSeek(cny, "u", now)
	if err != nil || s.Credits.Currency != "¥" || *s.Credits.Remaining != 7 {
		t.Fatalf("cny = %+v err=%v", s.Credits, err)
	}
	if _, err := ParseDeepSeek(map[string]any{"balance_infos": []any{map[string]any{"currency": "USD"}}}, "u", now); err == nil {
		t.Fatal("missing amounts must be an error, not a zero balance")
	}
	if _, err := ParseDeepSeek(map[string]any{"balance_infos": []any{map[string]any{"currency": "EUR", "total_balance": "1"}}}, "u", now); err == nil {
		t.Fatal("unsupported currency accepted")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func statusErr(code int) *httpx.StatusError {
	return &httpx.StatusError{Provider: "codex", Status: code}
}

func TestCodexFileFallbackIsReadOnlyAndIgnoresExpiredTokens(t *testing.T) {
	dir := t.TempDir()
	write := func(expires int64) string {
		p := filepath.Join(dir, "c.json")
		raw := fmt.Sprintf(`{"type":"oauth","access":"a","refresh":"r","expires":%d,"accountId":"acc"}`, expires)
		if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if a := CodexFileAt(write(now.Add(time.Hour).UnixMilli()))(now); a == nil || a.Token != "a" || a.AccountID != "acc" {
		t.Fatalf("valid file = %+v", a)
	}
	if a := CodexFileAt(write(now.Add(-time.Hour).UnixMilli()))(now); a != nil {
		t.Fatalf("expired token used: %+v", a)
	}
	if a := CodexFileAt(filepath.Join(dir, "missing.json"))(now); a != nil {
		t.Fatalf("missing file = %+v", a)
	}
}

func TestBillingSourcesMakeNoRequestWithoutCredential(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, fetch := range map[string]func(context.Context, quota.Env) (quota.Snapshot, error){
		"xai": fetchXAI(srv.URL, srv.URL), "openrouter": fetchOpenRouter(srv.URL, srv.URL), "deepseek": fetchDeepSeek(srv.URL), "codex": fetchCodex(srv.URL),
	} {
		s, err := fetch(context.Background(), env(""))
		if err != nil || !s.NeedsAuth || s.Error == "" {
			t.Fatalf("%s: %+v %v", name, s, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a signed-out provider made a request")
	}
}
