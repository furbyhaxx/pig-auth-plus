package claude

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

const oauthKeyMarker = "sk-ant-oat"

// Shaper turns PiG's Anthropic request into a Claude Code subscription request.
type Shaper struct {
	env      func(string) string
	rules    func() []config.ReplacePattern
	version  *version
	identity *identity

	mu         sync.Mutex
	preflights map[string]bool
	insecure   *http.Client
}

// NewShaper keeps the device identity under stateDir; rules returns the current
// claude.replacePatterns.
func NewShaper(stateDir func() (string, error), env func(string) string, rules func() []config.ReplacePattern) *Shaper {
	return &Shaper{env: env, rules: rules, version: newVersion(env), identity: newIdentity(stateDir), preflights: map[string]bool{}}
}

// VersionWarning reports a malformed or outdated version override after first use.
func (s *Shaper) VersionWarning() string {
	s.version.get()
	return s.version.warning
}

// oauthToken returns the subscription token of a request, from a Bearer header or an
// sk-ant-oat API key.
func oauthToken(options map[string]any) string {
	if h, ok := options["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok && strings.EqualFold(k, "authorization") && strings.HasPrefix(s, "Bearer ") {
				return strings.TrimPrefix(s, "Bearer ")
			}
		}
	}
	if key, ok := options["apiKey"].(string); ok && strings.Contains(key, oauthKeyMarker) {
		return key
	}
	return ""
}

func accountUUID(options map[string]any) string {
	if h, ok := options["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok && strings.EqualFold(k, AccountHeader) {
				return s
			}
		}
	}
	return ""
}

// Exchange returns the transport for one stream. API-key requests stay on PiG's Anthropic
// path unchanged; subscription requests get the full Claude Code shape.
func (s *Shaper) Exchange(options map[string]any, signal context.Context) (*http.Client, wire.Exchange) {
	token := oauthToken(options)
	if token == "" {
		return wire.Client, wire.Exchange{Signal: signal}
	}
	account := accountUUID(options)
	hostSession, _ := options["sessionId"].(string)
	session := s.identity.sessionID(hostSession)
	base := customBase(s.env)
	client := wire.Client
	if insecureTLS(s.env) {
		client = s.insecureClient()
	}
	var restore restorer
	return client, wire.Exchange{
		Signal: signal,
		Request: func(r *wire.Request) error {
			shapeURL(r.HTTP.URL, base)
			r.HTTP.Host = ""
			if r.HTTP.URL.Path == "/v1/messages" {
				s.preflight(client, r.HTTP.URL.Scheme+"://"+r.HTTP.URL.Host, account)
			}
			version := s.version.get()
			userID, _ := json.Marshal(struct {
				DeviceID    string `json:"device_id"`
				AccountUUID string `json:"account_uuid"`
				SessionID   string `json:"session_id"`
			}{s.identity.deviceID(), account, session})
			shapeHeaders(r.HTTP.Header, token, version, session)
			if len(r.Body) == 0 {
				return nil
			}
			body, rs, err := shapeBody(r.Body, version, s.rules(), string(userID))
			if err != nil {
				return err
			}
			r.Body, restore = body, rs
			return nil
		},
		Response: func(resp *http.Response) (*http.Response, error) {
			return restoreResponse(resp, restore), nil
		},
	}
}

// preflight sends the CLI's one-time `/api/hello` probe per origin and account, detached and
// bounded, ignoring failures.
func (s *Shaper) preflight(client *http.Client, origin, account string) {
	key := origin + "\x00" + account
	s.mu.Lock()
	sent := s.preflights[key]
	s.preflights[key] = true
	s.mu.Unlock()
	if sent {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, origin+"/api/hello", nil)
		if err != nil {
			return
		}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
}

func (s *Shaper) insecureClient() *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insecure == nil {
		tr := wire.Client.Transport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit ANTHROPIC_INSECURE for a custom endpoint
		s.insecure = &http.Client{Transport: tr}
	}
	return s.insecure
}
