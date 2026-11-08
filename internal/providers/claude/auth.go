package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/oauthx"
)

const (
	clientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	redirectURI = "https://platform.claude.com/oauth/code/callback"
	// AccountHeader carries the OAuth account UUID from ToAuth to the request shaper, which
	// removes it before the request leaves.
	AccountHeader = "x-auth-plus-account-uuid"
)

var scopes = []string{
	"org:create_api_key",
	"user:profile",
	"user:inference",
	"user:sessions:claude_code",
	"user:mcp_servers",
	"user:file_upload",
}

// Endpoints are the OAuth URLs; tests point them at a local server.
type Endpoints struct {
	MaxAuthorize     string
	ConsoleAuthorize string
	Token            string
	CreateKey        string
}

// DefaultEndpoints are Anthropic's production URLs.
var DefaultEndpoints = Endpoints{
	MaxAuthorize:     "https://claude.ai/oauth/authorize",
	ConsoleAuthorize: "https://platform.claude.com/oauth/authorize",
	Token:            "https://platform.claude.com/v1/oauth/token",
	CreateKey:        "https://api.anthropic.com/api/oauth/claude_cli/create_api_key",
}

// Auth implements Claude Pro/Max OAuth and Anthropic API-key login.
type Auth struct {
	Endpoints Endpoints
	Now       func() time.Time
	// RetryDelay is the first refresh retry delay; it doubles per attempt.
	RetryDelay time.Duration
}

// AuthError is a failed credential operation; it never carries tokens or provider bodies.
type AuthError struct {
	Operation string
	Endpoint  string
	Status    int
}

func (e *AuthError) Error() string {
	s := fmt.Sprintf("claude: %s at %s", e.Operation, e.Endpoint)
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	return s + "; run /login claude again"
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func failure(op, endpoint string, err error) error {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return &AuthError{Operation: op, Endpoint: endpoint, Status: se.Status}
	}
	return &AuthError{Operation: op, Endpoint: endpoint}
}

func tokenHeaders() http.Header {
	return http.Header{
		"Content-Type": {"application/json"},
		"Accept":       {"application/json, text/plain, */*"},
		"User-Agent":   {"axios/1.13.6"},
	}
}

type tokenResponse struct {
	Access    string   `json:"access_token"`
	Refresh   string   `json:"refresh_token"`
	ExpiresIn *float64 `json:"expires_in"`
	Account   *struct {
		UUID string `json:"uuid"`
	} `json:"account"`
}

func (a *Auth) parseTokens(t tokenResponse) (*sdk.Credential, error) {
	if strings.TrimSpace(t.Access) == "" || strings.TrimSpace(t.Refresh) == "" || t.ExpiresIn == nil || *t.ExpiresIn <= 0 {
		return nil, &AuthError{Operation: "invalid OAuth token response", Endpoint: a.Endpoints.Token}
	}
	expires := a.now().UnixMilli() + int64(*t.ExpiresIn*1000)
	c := sdk.NewCredential("type", "oauth", "refresh", t.Refresh, "access", t.Access, "expires", expires)
	if t.Account != nil && t.Account.UUID != "" {
		c.Set("accountUuid", t.Account.UUID)
	}
	return c, nil
}

func (a *Auth) postToken(ctx context.Context, body map[string]string, secrets ...string) (tokenResponse, error) {
	raw, _ := json.Marshal(body)
	var out tokenResponse
	_, err := httpx.JSON(ctx, httpx.Request{Provider: "claude", Method: http.MethodPost, URL: a.Endpoints.Token, Header: tokenHeaders(), Body: raw, Secrets: secrets}, &out)
	return out, err
}

// login runs the PKCE flow with a pasted code against the Max or Console authorize page.
func (a *Auth) login(in sdk.AuthInteraction, console bool) (*sdk.Credential, error) {
	ctx := signal(in)
	pkce := oauthx.NewPKCE()
	state := oauthx.RandomState()
	base := a.Endpoints.MaxAuthorize
	if console {
		base = a.Endpoints.ConsoleAuthorize
	}
	q := url.Values{
		"code":                  {"true"},
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if err := in.Notify(map[string]any{"type": "auth_url", "url": base + "?" + q.Encode(), "instructions": "Paste the authorization code here:"}); err != nil {
		return nil, err
	}
	input, err := in.Prompt(map[string]any{"type": "manual_code", "message": "Paste the authorization code:"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paste, ok := oauthx.ParseCodePaste(input)
	if !ok || paste.State != state {
		return nil, &AuthError{Operation: "invalid authorization callback or state", Endpoint: redirectURI}
	}
	tokens, err := a.postToken(ctx, map[string]string{
		"code":          paste.Code,
		"state":         state,
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"redirect_uri":  redirectURI,
		"code_verifier": pkce.Verifier,
	}, paste.Code, pkce.Verifier)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("authorization exchange failed", a.Endpoints.Token, err)
	}
	return a.parseTokens(tokens)
}

// Refresh exchanges the refresh token; 5xx and network failures retry twice.
func (a *Auth) Refresh(cred *sdk.Credential, ctx context.Context) (*sdk.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	refresh, access := cred.String("refresh"), cred.String("access")
	delay := a.RetryDelay
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay << (attempt - 1)):
			}
		}
		tokens, err := a.postToken(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": refresh, "client_id": clientID}, refresh, access)
		if err == nil {
			next, err := a.parseTokens(tokens)
			if err != nil {
				return nil, err
			}
			out := cred.Clone()
			for _, k := range next.Keys() {
				v, _ := next.Get(k)
				out.Set(k, v)
			}
			return out, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt < 2 && retryable(err) {
			continue
		}
		return nil, failure("token refresh failed", a.Endpoints.Token, err)
	}
}

func retryable(err error) bool {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return se.Status >= 500
	}
	var ae *AuthError
	return !errors.As(err, &ae)
}

// ToAuth sends the access token as the API key; PiG's Anthropic API recognizes OAuth tokens.
func ToAuth(cred *sdk.Credential) (map[string]any, error) {
	access := cred.String("access")
	if access == "" {
		return nil, errors.New("claude: stored OAuth credential has no access token; run /login claude")
	}
	auth := map[string]any{"apiKey": access}
	if uuid := cred.String("accountUuid"); uuid != "" {
		auth["headers"] = map[string]any{AccountHeader: uuid}
	}
	return auth, nil
}

// LoginAPIKey asks for a key or creates one through the Console OAuth flow.
func (a *Auth) LoginAPIKey(in sdk.AuthInteraction) (*sdk.Credential, error) {
	ctx := signal(in)
	method, err := in.Prompt(map[string]any{"type": "select", "message": "API key method:", "options": []map[string]any{
		{"id": "manual", "label": "Manually enter API Key"},
		{"id": "create", "label": "Create an API Key"},
	}})
	if err != nil {
		return nil, err
	}
	switch method {
	case "manual":
		key, err := in.Prompt(map[string]any{"type": "secret", "message": "Enter Anthropic API key:"})
		if err != nil {
			return nil, err
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, &AuthError{Operation: "API key cannot be empty", Endpoint: "manual entry"}
		}
		return sdk.NewCredential("type", "api_key", "key", key), nil
	case "create":
	default:
		return nil, sdk.ErrOAuthCancelled
	}
	cred, err := a.login(in, true)
	if err != nil {
		return nil, err
	}
	access, refresh := cred.String("access"), cred.String("refresh")
	var out struct {
		RawKey string `json:"raw_key"`
	}
	_, err = httpx.JSON(ctx, httpx.Request{
		Provider: "claude", Method: http.MethodPost, URL: a.Endpoints.CreateKey,
		Header:  http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + access}},
		Secrets: []string{access, refresh},
	}, &out)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("API key creation failed", a.Endpoints.CreateKey, err)
	}
	if strings.TrimSpace(out.RawKey) == "" {
		return nil, &AuthError{Operation: "invalid API key creation response", Endpoint: a.Endpoints.CreateKey}
	}
	return sdk.NewCredential("type", "api_key", "key", out.RawKey), nil
}

// ResolveAPIKey prefers the stored key, then ANTHROPIC_API_KEY.
func (a *Auth) ResolveAPIKey(in sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
	if key := in.Credential.String("key"); key != "" {
		src := "stored API key"
		return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}, Source: &src}, nil
	}
	if in.Ctx.Env != nil {
		v, err := in.Ctx.Env("ANTHROPIC_API_KEY")
		if err != nil {
			return nil, err
		}
		if v != nil && *v != "" {
			src := "ANTHROPIC_API_KEY"
			return &sdk.AuthResult{Auth: map[string]any{"apiKey": *v}, Source: &src}, nil
		}
	}
	return nil, nil
}

// ProviderAuth assembles both login methods.
func (a *Auth) ProviderAuth() sdk.ProviderAuth {
	sub := true
	label := "Claude Pro/Max"
	return sdk.ProviderAuth{
		OAuth: &sdk.OAuthAuth{
			Name: "Claude Pro/Max", IsSubscription: &sub, LoginLabel: &label,
			Login:   func(in sdk.AuthInteraction, _ sdk.LoginOptions) (*sdk.Credential, error) { return a.login(in, false) },
			Refresh: a.Refresh,
			ToAuth:  ToAuth,
		},
		APIKey: &sdk.APIKeyAuth{Name: "Claude API key", Login: a.LoginAPIKey, Resolve: a.ResolveAPIKey},
	}
}

func signal(in sdk.AuthInteraction) context.Context {
	if in.Signal != nil {
		return in.Signal
	}
	return context.Background()
}
