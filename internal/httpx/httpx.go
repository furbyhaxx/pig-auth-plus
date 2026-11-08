// Package httpx performs pig-auth-plus' own HTTP requests (auth, quotas, catalogs) with
// deadlines, typed failures and secret redaction.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout bounds a request when the caller sets none.
const DefaultTimeout = 30 * time.Second

const maxErrorBody = 2000

// Client is the shared client for non-streaming requests; deadlines come from contexts.
var Client = &http.Client{}

// StatusError is a non-2xx response. Body is redacted and truncated.
type StatusError struct {
	Provider   string
	Status     int
	URL        string
	Body       string
	RetryAfter time.Duration
	// HasRetryAfter distinguishes an absent Retry-After from a zero delay.
	HasRetryAfter bool
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s: HTTP %d from %s", e.Provider, e.Status, e.URL)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// TimeoutError is the request's own deadline, distinct from caller cancellation.
type TimeoutError struct {
	Provider string
	URL      string
	Timeout  time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s: no response from %s within %s; retry later", e.Provider, e.URL, e.Timeout)
}

// Request describes one call. Secrets are scrubbed from every error.
type Request struct {
	Provider string
	Method   string
	URL      string
	Header   http.Header
	Body     []byte
	Timeout  time.Duration
	Secrets  []string
}

// Do sends r and returns the response with its body fully read. Non-2xx statuses become a
// [*StatusError]; caller cancellation returns ctx's error unchanged.
func Do(ctx context.Context, r Request) (*http.Response, []byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	safeURL := Redact(r.URL, r.Secrets...)
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if r.Body != nil {
		body = strings.NewReader(string(r.Body))
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(reqCtx, method, r.URL, body)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: build request to %s: %s", r.Provider, safeURL, Redact(err.Error(), r.Secrets...))
	}
	if r.Header != nil {
		req.Header = r.Header.Clone()
	}
	resp, err := Client.Do(req)
	if err == nil {
		var data []byte
		data, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				text := string(data)
				if len(text) > maxErrorBody {
					text = text[:maxErrorBody]
				}
				se := &StatusError{Provider: r.Provider, Status: resp.StatusCode, URL: safeURL, Body: Redact(strings.TrimSpace(text), r.Secrets...)}
				se.RetryAfter, se.HasRetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
				return resp, data, se
			}
			return resp, data, nil
		}
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return nil, nil, &TimeoutError{Provider: r.Provider, URL: safeURL, Timeout: timeout}
	}
	return nil, nil, fmt.Errorf("%s: request to %s failed: %s", r.Provider, safeURL, Redact(err.Error(), r.Secrets...))
}

// JSON sends r and decodes a 2xx body into out.
func JSON(ctx context.Context, r Request, out any) (*http.Response, error) {
	resp, data, err := Do(ctx, r)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp, fmt.Errorf("%s: invalid JSON from %s", r.Provider, Redact(r.URL, r.Secrets...))
	}
	return resp, nil
}

// ParseRetryAfter reads delay-seconds or an HTTP date; the delay is never negative.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(value, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second)), true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(0, at.Sub(now)), true
}

// Redacted replaces secrets in text.
const Redacted = "[redacted]"

const minExactSecret = 8

var (
	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+=*`),
		regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`\bya29\.[A-Za-z0-9._-]+`),
		regexp.MustCompile(`\b1//[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`\bxai-[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`\bsyn_[A-Za-z0-9]{16,}`),
	}
	jsonField  = regexp.MustCompile(`("(?:access_token|refresh_token|id_token|api_key|apiKey|client_secret|password|token|raw_key)"\s*:\s*")[^"]*(")`)
	queryField = regexp.MustCompile(`(?i)([?&](?:key|api_key|access_token|token|code)=)[^&#\s]+`)
)

// Redact removes known secret values and recognizable token shapes from text.
func Redact(text string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) >= minExactSecret {
			text = strings.ReplaceAll(text, s, Redacted)
		}
	}
	for _, p := range tokenPatterns {
		text = p.ReplaceAllString(text, Redacted)
	}
	text = jsonField.ReplaceAllString(text, "${1}"+Redacted+"${2}")
	return queryField.ReplaceAllString(text, "${1}"+Redacted)
}
