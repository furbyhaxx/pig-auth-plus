package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func tokenServer(t *testing.T, handle func(body map[string]string) (int, string)) (*httptest.Server, *Auth) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		status, out := handle(body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	now := time.UnixMilli(1_000_000)
	return srv, &Auth{
		Endpoints:  Endpoints{MaxAuthorize: srv.URL + "/authorize", ConsoleAuthorize: srv.URL + "/console", Token: srv.URL + "/token", CreateKey: srv.URL + "/key"},
		Now:        func() time.Time { return now },
		RetryDelay: time.Millisecond,
	}
}

func TestLoginExchangesPastedCodeWithMatchingState(t *testing.T) {
	var verifier string
	_, a := tokenServer(t, func(b map[string]string) (int, string) {
		if b["grant_type"] != "authorization_code" || b["code"] != "the-code" {
			return 400, `{}`
		}
		verifier = b["code_verifier"]
		return 200, `{"access_token":"sk-ant-oat-a","refresh_token":"r1","expires_in":3600,"account":{"uuid":"acct"}}`
	})
	var authURL string
	in := sdk.AuthInteraction{
		Signal: context.Background(),
		Notify: func(m map[string]any) error { authURL, _ = m["url"].(string); return nil },
		Prompt: func(map[string]any) (string, error) {
			u, _ := url.Parse(authURL)
			return "the-code#" + u.Query().Get("state"), nil
		},
	}
	cred, err := a.ProviderAuth().OAuth.Login(in, sdk.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.String("access") != "sk-ant-oat-a" || cred.String("accountUuid") != "acct" || verifier == "" {
		t.Fatalf("credential = %v verifier=%q", cred, verifier)
	}
	if exp, _ := cred.Get("expires"); exp != int64(1_000_000+3_600_000) {
		t.Fatalf("expires = %v", exp)
	}
	auth, _ := ToAuth(cred)
	if auth["apiKey"] != "sk-ant-oat-a" {
		t.Fatalf("toAuth = %v", auth)
	}
}

func TestLoginRejectsStateMismatch(t *testing.T) {
	_, a := tokenServer(t, func(map[string]string) (int, string) { return 200, `{}` })
	in := sdk.AuthInteraction{
		Notify: func(map[string]any) error { return nil },
		Prompt: func(map[string]any) (string, error) { return "code#wrong", nil },
	}
	_, err := a.ProviderAuth().OAuth.Login(in, sdk.LoginOptions{})
	var ae *AuthError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshRetriesServerErrorsButNotClientErrors(t *testing.T) {
	var calls atomic.Int32
	_, a := tokenServer(t, func(b map[string]string) (int, string) {
		if calls.Add(1) == 1 {
			return 503, `busy`
		}
		return 200, `{"access_token":"new","refresh_token":"r2","expires_in":60}`
	})
	old := sdk.NewCredential("type", "oauth", "refresh", "r1", "access", "old", "expires", int64(1), "accountUuid", "acct")
	got, err := a.Refresh(old, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.String("access") != "new" || got.String("refresh") != "r2" || got.String("accountUuid") != "acct" || calls.Load() != 2 {
		t.Fatalf("got %v after %d calls", got, calls.Load())
	}

	calls.Store(0)
	_, b := tokenServer(t, func(map[string]string) (int, string) {
		calls.Add(1)
		return 400, `{"error":"invalid_grant","refresh_token":"r1"}`
	})
	_, err = b.Refresh(old, context.Background())
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != 400 || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if strings.Contains(err.Error(), "r1") {
		t.Fatalf("refresh token leaked: %v", err)
	}
}
