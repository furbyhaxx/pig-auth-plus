package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

const (
	countTimeout    = 10 * time.Second
	countMemoLimit  = 256
	countConcurrent = 2
)

var errCaptured = errors.New("claude count request captured")

var needsAuth = tokens.Diagnostic{Provider: ProviderID, Message: "Claude token counting needs authentication; run /login claude."}

// Counter counts a complete Claude request through the native count_tokens endpoint. The body
// is the one a real turn would send, captured from PiG's serializer after this package's
// shaping, so framing, system updates and tool definitions are all included.
type Counter struct {
	Provider *Provider
	// Token returns the credential PiG resolves for claude; empty when signed out.
	Token func(ctx context.Context) (string, error)
	// Model returns the catalog model object for an id.
	Model func(id string) (map[string]any, error)

	sem       chan struct{}
	mu        sync.Mutex
	backoff   time.Time
	memo      map[string]int
	memoOrder []string
}

// Text implements tokens.Counter with the measured Claude estimate.
func (*Counter) Text(_ context.Context, text string, k tokens.Kind) tokens.Count {
	return tokens.RatiosFor(ProviderID, "").Estimate(text, k)
}

// AnchorSource implements tokens.Counter.
func (*Counter) AnchorSource() tokens.Source { return tokens.SourceProvider }

// SystemText implements tokens.SystemRewriter: the system prompt as the subscription sends it.
func (c *Counter) SystemText(text string) string {
	return SanitizeSystemText(text, c.Provider.Shaper.rules())
}

// Context implements tokens.NativeCounter. It never fails: false keeps the labelled estimate.
func (c *Counter) Context(ctx context.Context, in tokens.NativeInput) (tokens.Count, bool) {
	// The endpoint rejects an empty conversation, so there is nothing to count natively.
	if len(in.Messages) == 0 {
		return tokens.Count{}, false
	}
	token, err := c.Token(ctx)
	if err != nil || ctx.Err() != nil {
		return tokens.Count{}, false
	}
	if token == "" {
		return tokens.Count{Source: tokens.SourceProvider, Diagnostics: []tokens.Diagnostic{needsAuth}}, false
	}
	c.mu.Lock()
	if c.sem == nil {
		c.sem = make(chan struct{}, countConcurrent)
		c.memo = map[string]int{}
	}
	blocked := time.Now().Before(c.backoff)
	c.mu.Unlock()
	if blocked {
		return tokens.Count{}, false
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return tokens.Count{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()
	model, err := c.Model(in.Model)
	if err != nil || model == nil || model["api"] != "anthropic-messages" || model["provider"] != ProviderID {
		return tokens.Count{}, false
	}
	if reasoning, _ := model["reasoning"].(bool); reasoning && in.ThinkingLevel == "" {
		return tokens.Count{}, false
	}
	req, ok := c.capture(ctx, token, model, in)
	if !ok {
		return tokens.Count{}, false
	}
	key := req.key()
	c.mu.Lock()
	cached, hit := c.memo[key]
	c.mu.Unlock()
	if hit {
		return tokens.Count{Tokens: cached, Source: tokens.SourceEndpoint}, true
	}
	n, err := c.post(ctx, token, req)
	if err != nil {
		var se *httpx.StatusError
		wait := time.Minute
		if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
			wait = 10 * time.Minute
		}
		if ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.mu.Lock()
			c.backoff = time.Now().Add(wait)
			c.mu.Unlock()
		}
		return tokens.Count{}, false
	}
	c.remember(key, n)
	return tokens.Count{Tokens: n, Source: tokens.SourceEndpoint}, true
}

type countRequest struct {
	url    string
	header http.Header
	body   []byte
}

func (r countRequest) key() string {
	return r.url + "\x00" + string(r.body) + "\x00" + r.header.Get("Anthropic-Beta")
}

func (c *Counter) remember(key string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.memo[key]; !ok {
		c.memoOrder = append(c.memoOrder, key)
	}
	c.memo[key] = n
	if len(c.memoOrder) > countMemoLimit {
		delete(c.memo, c.memoOrder[0])
		c.memoOrder = c.memoOrder[1:]
	}
}

// capture serializes the request exactly as a turn would, then stops before the network.
func (c *Counter) capture(ctx context.Context, token string, model map[string]any, in tokens.NativeInput) (countRequest, bool) {
	messages := make([]any, len(in.Messages))
	for i, m := range in.Messages {
		messages[i] = m
	}
	request := map[string]any{"messages": messages}
	if in.SystemPrompt != "" {
		request["systemPrompt"] = in.SystemPrompt
	}
	if len(in.Tools) > 0 {
		tools := make([]any, len(in.Tools))
		for i, t := range in.Tools {
			tools[i] = map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}
		}
		request["tools"] = tools
	}
	options := map[string]any{"apiKey": token}
	if in.ThinkingLevel != "" && in.ThinkingLevel != "off" {
		options["reasoning"] = in.ThinkingLevel
	}
	client, ex := c.Provider.Shaper.Exchange(options, ctx)
	shape := ex.Request
	var got *countRequest
	ex.Request = func(r *wire.Request) error {
		if shape != nil {
			if err := shape(r); err != nil {
				return err
			}
		}
		got = &countRequest{url: r.HTTP.URL.String(), header: r.HTTP.Header.Clone(), body: bytes.Clone(r.Body)}
		return errCaptured
	}
	options["fetch"] = wire.Fetch(client, ex)
	stream, err := c.Provider.Runtime.Stream(wire.Provider{Name: wireID, Config: c.Provider.Catalog.wireConfig()}, wire.Call{Simple: true, Model: model, Request: request, Options: options, Signal: ctx})
	if err != nil {
		return countRequest{}, false
	}
	stream.Result()
	if got == nil || ctx.Err() != nil {
		return countRequest{}, false
	}
	return adaptCount(*got, token, oauthToken(options) != "")
}

// adaptCount turns a messages request into a count_tokens request: generation-only fields go,
// and so does the billing block, which the count endpoint does not take.
func adaptCount(r countRequest, token string, oauth bool) (countRequest, bool) {
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil || body == nil {
		return countRequest{}, false
	}
	for _, k := range []string{"stream", "max_tokens", "metadata", "temperature", "fallbacks", "betas"} {
		delete(body, k)
	}
	if system, ok := body["system"].([]any); ok && oauth && len(system) > 0 {
		if first, _ := system[0].(map[string]any); first != nil {
			if text, _ := first["text"].(string); strings.HasPrefix(text, "x-anthropic-billing-header:") {
				body["system"] = system[1:]
			}
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return countRequest{}, false
	}
	u, err := url.Parse(r.url)
	if err != nil || !strings.HasSuffix(u.Path, "/v1/messages") {
		return countRequest{}, false
	}
	u.Path += "/count_tokens"
	h := r.header.Clone()
	for _, k := range []string{"Content-Length", "Accept-Encoding", "Accept"} {
		h.Del(k)
	}
	h.Set("Content-Type", "application/json")
	if !oauth && h.Get("X-Api-Key") == "" {
		h.Set("X-Api-Key", token)
	}
	return countRequest{url: u.String(), header: h, body: bytes.TrimSuffix(out.Bytes(), []byte("\n"))}, true
}

func (c *Counter) post(ctx context.Context, token string, r countRequest) (int, error) {
	var out struct {
		InputTokens *int `json:"input_tokens"`
	}
	_, err := httpx.JSON(ctx, httpx.Request{
		Provider: ProviderID, Method: http.MethodPost, URL: r.url, Header: r.header, Body: r.body,
		Timeout: countTimeout, Secrets: []string{token},
	}, &out)
	if err != nil {
		return 0, err
	}
	if out.InputTokens == nil || *out.InputTokens < 0 {
		return 0, errors.New("claude: count response has no input_tokens")
	}
	return *out.InputTokens, nil
}
