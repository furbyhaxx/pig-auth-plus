// Package sources holds one quota fetcher per provider. Each parser turns a recorded
// provider payload into a [quota.Snapshot]; unknown numbers stay nil.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

const requestTimeout = 10 * time.Second

// obj returns v as a JSON object, or nil.
func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// num returns a finite JSON number, or nil. Numeric strings are not numbers.
func num(v any) *float64 {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

// numish also accepts numeric strings, for APIs that quote their amounts.
func numish(v any) *float64 {
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return &f
	}
	return num(v)
}

// text returns a non-blank string, or "".
func text(v any) string {
	s, _ := v.(string)
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// frac clamps to [0,1]; nil stays unknown.
func frac(f *float64) *float64 {
	if f == nil {
		return nil
	}
	return quota.Frac(math.Max(0, math.Min(1, *f)))
}

// percent turns 0..100 into a used fraction.
func percent(f *float64) *float64 {
	if f == nil {
		return nil
	}
	return frac(quota.Frac(*f / 100))
}

// timeMs reads an RFC 3339 string or an epoch in seconds or milliseconds.
func timeMs(v any) *int64 {
	switch x := v.(type) {
	case float64:
		return epoch(x)
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return epoch(f)
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return quota.Ms(t.UnixMilli())
		}
	}
	return nil
}

func epoch(f float64) *int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return nil
	}
	if f > 1e12 {
		return quota.Ms(int64(f))
	}
	return quota.Ms(int64(f * 1000))
}

// needsAuth is the snapshot of a provider without a credential; no request was made.
func needsAuth(provider string, now time.Time, hint string) quota.Snapshot {
	return quota.Snapshot{Provider: provider, FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, NeedsAuth: true, Error: hint}
}

// getJSON sends one authenticated request and decodes a JSON object; HTTP failures keep their
// *httpx.StatusError so the engine can back off.
func getJSON(ctx context.Context, provider, method, url string, header http.Header, body []byte, secrets ...string) (map[string]any, error) {
	var out any
	_, err := httpx.JSON(ctx, httpx.Request{Provider: provider, Method: method, URL: url, Header: header, Body: body, Timeout: requestTimeout, Secrets: secrets}, &out)
	if err != nil {
		return nil, err
	}
	m := obj(out)
	if m == nil {
		return nil, fmt.Errorf("%s: %s returned JSON that is not an object; retry later", provider, httpx.Redact(url, secrets...))
	}
	return m, nil
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}}
}

// decode parses a recorded payload for tests and parsers fed raw bytes.
func decode(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}
