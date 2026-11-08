package antigravity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/oauthx"
)

const (
	clientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	// clientSecret is the installed-app protocol constant shared with the public CLI.
	clientSecret = "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"
	redirectURI  = "https://antigravity.google/oauth-callback"
)

var scopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
	"https://www.googleapis.com/auth/aicode",
	"openid",
}

var bareCode = regexp.MustCompile(`^[A-Za-z0-9_./~-]+$`)

// Endpoints are the OAuth URLs; tests point them at a local server.
type Endpoints struct {
	Authorize string
	Token     string
}

// DefaultEndpoints are Google's production URLs.
var DefaultEndpoints = Endpoints{
	Authorize: "https://accounts.google.com/o/oauth2/auth",
	Token:     "https://oauth2.googleapis.com/token",
}

// Auth implements Antigravity's consumer Google OAuth.
type Auth struct {
	Endpoints  Endpoints
	Now        func() time.Time
	RetryDelay time.Duration
}

// AuthError is a failed credential operation; it never carries tokens or provider bodies.
type AuthError struct {
	Operation string
	Endpoint  string
	Status    int
}

func (e *AuthError) Error() string {
	s := fmt.Sprintf("antigravity: %s at %s", e.Operation, e.Endpoint)
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	return s + "; run /login antigravity again"
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Auth) failure(op string, err error) error {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return &AuthError{Operation: op, Endpoint: a.Endpoints.Token, Status: se.Status}
	}
	var ae *AuthError
	if errors.As(err, &ae) {
		return ae
	}
	return &AuthError{Operation: op, Endpoint: a.Endpoints.Token}
}

type tokenResponse struct {
	Access    string   `json:"access_token"`
	Refresh   string   `json:"refresh_token"`
	ExpiresIn *float64 `json:"expires_in"`
}

func (a *Auth) exchange(ctx context.Context, form url.Values, secrets ...string) (*sdk.Credential, error) {
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	var t tokenResponse
	_, err := httpx.JSON(ctx, httpx.Request{
		Provider: ID, Method: http.MethodPost, URL: a.Endpoints.Token,
		Header:  http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:    []byte(form.Encode()),
		Secrets: append(secrets, clientSecret),
	}, &t)
	if err != nil {
		return nil, err
	}
	refresh := t.Refresh
	if refresh == "" {
		refresh = form.Get("refresh_token")
	}
	if strings.TrimSpace(t.Access) == "" || t.ExpiresIn == nil || *t.ExpiresIn <= 0 {
		return nil, &AuthError{Operation: "invalid OAuth token response", Endpoint: a.Endpoints.Token}
	}
	if strings.TrimSpace(refresh) == "" {
		return nil, &AuthError{Operation: "missing OAuth refresh token", Endpoint: a.Endpoints.Token}
	}
	expires := a.now().UnixMilli() + int64(*t.ExpiresIn*1000)
	return sdk.NewCredential("type", "oauth", "refresh", refresh, "access", t.Access, "expires", expires), nil
}

// Login runs PKCE with a pasted callback URL or code.
func (a *Auth) Login(in sdk.AuthInteraction, _ sdk.LoginOptions) (*sdk.Credential, error) {
	ctx := oauthx.Context(in)
	pkce := oauthx.NewPKCE()
	state := oauthx.RandomState()
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}
	if err := in.Notify(map[string]any{"type": "auth_url", "url": a.Endpoints.Authorize + "?" + q.Encode(), "instructions": "Paste the redirected URL or authorization code here:"}); err != nil {
		return nil, err
	}
	input, err := in.Prompt(map[string]any{"type": "manual_code", "message": "Paste the Antigravity callback URL or code:"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input = strings.TrimSpace(input)
	code := ""
	// A bare code has no returned state; PKCE still binds it to this attempt.
	if paste, ok := oauthx.ParseCodePaste(input); ok && paste.State != "" {
		if paste.State == state {
			code = paste.Code
		}
	} else if bareCode.MatchString(input) {
		code = input
	}
	if code == "" {
		return nil, &AuthError{Operation: "invalid authorization callback or state", Endpoint: redirectURI}
	}
	cred, err := a.exchange(ctx, url.Values{
		"code": {code}, "grant_type": {"authorization_code"},
		"redirect_uri": {redirectURI}, "code_verifier": {pkce.Verifier},
	}, code, pkce.Verifier)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, a.failure("authorization exchange failed", err)
	}
	return cred, nil
}

// Refresh exchanges the refresh token; 5xx and network failures retry twice. A refresh token
// from pi-antigravity-auth carries a "|project" suffix, which is dropped.
func (a *Auth) Refresh(cred *sdk.Credential, ctx context.Context) (*sdk.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	refresh, _, _ := strings.Cut(cred.String("refresh"), "|")
	access := cred.String("access")
	var next *sdk.Credential
	permanent := func(err error) bool { var ae *AuthError; return errors.As(err, &ae) }
	err := oauthx.Retry(ctx, a.RetryDelay, permanent, func() (err error) {
		next, err = a.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, refresh, access)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, a.failure("token refresh failed", err)
	}
	return next, nil
}

// ToAuth sends the access token as a bearer header.
func ToAuth(cred *sdk.Credential) (map[string]any, error) {
	access := cred.String("access")
	if access == "" {
		return nil, errors.New("antigravity: stored OAuth credential has no access token; run /login antigravity")
	}
	return map[string]any{"apiKey": access, "headers": map[string]any{"Authorization": "Bearer " + access}}, nil
}
