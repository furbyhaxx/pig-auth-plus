// Package oauthx holds vendor-neutral OAuth helpers: PKCE, state and pasted-code parsing.
package oauthx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

// PKCE is an S256 verifier and challenge pair.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE draws 32 random bytes for the verifier.
func NewPKCE() PKCE {
	v := RandomState()
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

// RandomState is an opaque 32-byte base64url value.
func RandomState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand never fails on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}

// CodePaste is an authorization code and its state.
type CodePaste struct {
	Code  string
	State string
}

// ParseCodePaste accepts a callback URL, a `code#state` pair or a query string.
func ParseCodePaste(input string) (CodePaste, bool) {
	s := strings.TrimSpace(input)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
		q := u.Query()
		if c, st := q.Get("code"), q.Get("state"); c != "" && st != "" {
			return CodePaste{c, st}, true
		}
	}
	if parts := strings.Split(s, "#"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return CodePaste{parts[0], parts[1]}, true
	}
	q, err := url.ParseQuery(strings.TrimPrefix(s, "?"))
	if err == nil {
		if c, st := q.Get("code"), q.Get("state"); c != "" && st != "" {
			return CodePaste{c, st}, true
		}
	}
	return CodePaste{}, false
}
