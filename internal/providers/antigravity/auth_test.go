package antigravity

import (
	"context"
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

func tokenServer(t *testing.T, handle func(form url.Values) (int, string)) *Auth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		status, out := handle(form)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	now := time.UnixMilli(1_000_000)
	return &Auth{Endpoints: Endpoints{Authorize: srv.URL + "/auth", Token: srv.URL + "/token"}, Now: func() time.Time { return now }, RetryDelay: time.Millisecond}
}

func interaction(answer func(authURL string) string) sdk.AuthInteraction {
	var authURL string
	return sdk.AuthInteraction{
		Signal: context.Background(),
		Notify: func(m map[string]any) error { authURL, _ = m["url"].(string); return nil },
		Prompt: func(map[string]any) (string, error) { return answer(authURL), nil },
	}
}

func TestLoginAcceptsCallbackURLAndBareCode(t *testing.T) {
	a := tokenServer(t, func(f url.Values) (int, string) {
		if f.Get("code") != "c1" || f.Get("code_verifier") == "" || f.Get("client_secret") == "" {
			return 400, `{}`
		}
		return 200, `{"access_token":"a1","refresh_token":"r1","expires_in":3600}`
	})
	callback := func(u string) string {
		parsed, _ := url.Parse(u)
		return redirectURI + "?code=c1&state=" + parsed.Query().Get("state")
	}
	for name, answer := range map[string]func(string) string{"callback": callback, "bare": func(string) string { return "c1" }} {
		cred, err := a.Login(interaction(answer), sdk.LoginOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cred.String("access") != "a1" || cred.String("refresh") != "r1" {
			t.Fatalf("%s: %v", name, cred)
		}
	}
	_, err := a.Login(interaction(func(string) string { return redirectURI + "?code=c1&state=other" }), sdk.LoginOptions{})
	var ae *AuthError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "state") {
		t.Fatalf("state mismatch: %v", err)
	}
}

func TestRefreshKeepsTokenStripsForkSuffixAndRetries5xx(t *testing.T) {
	var calls atomic.Int32
	a := tokenServer(t, func(f url.Values) (int, string) {
		if calls.Add(1) == 1 {
			return 503, `{}`
		}
		if f.Get("refresh_token") != "r1" {
			return 400, `{}`
		}
		return 200, `{"access_token":"a2","expires_in":60}`
	})
	cred, err := a.Refresh(sdk.NewCredential("type", "oauth", "refresh", "r1|proj-1", "access", "a1"), context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cred.String("access") != "a2" || cred.String("refresh") != "r1" || calls.Load() != 2 {
		t.Fatalf("cred=%v calls=%d", cred, calls.Load())
	}
}

func TestRefreshDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	a := tokenServer(t, func(url.Values) (int, string) { calls.Add(1); return 400, `{"error":"invalid_grant"}` })
	_, err := a.Refresh(sdk.NewCredential("refresh", "r1"), context.Background())
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != 400 || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestProjectsShareDiscoveryAndDoNotCacheFailure(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"proj-9"}`))
	}))
	defer srv.Close()
	p := &Projects{BaseURL: srv.URL}
	var pe *ProjectError
	if _, err := p.Resolve(context.Background(), "tok"); !errors.As(err, &pe) || pe.Status != 500 {
		t.Fatalf("failure: %v", err)
	}
	fail.Store(false)
	for range 3 {
		got, err := p.Resolve(context.Background(), "tok")
		if err != nil || got != "proj-9" {
			t.Fatalf("got %q %v", got, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	p.Reset()
	_, _ = p.Resolve(context.Background(), "tok")
	if calls.Load() != 3 {
		t.Fatalf("after reset calls = %d", calls.Load())
	}
}
