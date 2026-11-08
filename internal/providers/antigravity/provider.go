package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// Provider wires the catalog, OAuth, project discovery and Cloud Code envelope to a native
// PiG provider.
type Provider struct {
	Catalog  *Catalog
	Auth     *Auth
	Projects *Projects
	// Runtime supplies only the session's system prompt section order (wire.Runtime.Ordered).
	Runtime  *wire.Runtime
	Sessions *Sessions
	// Client is the HTTP client for inference; nil uses wire.Client.
	Client *http.Client
	// BaseURL overrides Cloud Code's origin in tests.
	BaseURL string
}

// Native builds the native provider registration.
func (p *Provider) Native() *sdk.Provider {
	base := p.base()
	sub := true
	label := "Antigravity (Google account)"
	fetcher := Fetcher{BaseURL: p.BaseURL, Projects: p.Projects}
	return &sdk.Provider{
		ID: ID, Name: "Antigravity", BaseURL: &base,
		Auth: sdk.ProviderAuth{OAuth: &sdk.OAuthAuth{
			Name: "Antigravity", IsSubscription: &sub, LoginLabel: &label,
			Login: p.Auth.Login, Refresh: p.Auth.Refresh, ToAuth: ToAuth,
		}},
		GetModels: func() ([]map[string]any, error) { return p.Catalog.Models(), nil },
		RefreshModels: func(r sdk.RefreshModelsContext) error {
			return p.Catalog.Refresh(r, fetcher.Fetch, time.Now())
		},
		Stream:       p.streamer(false),
		StreamSimple: p.streamer(true),
	}
}

func (p *Provider) base() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return BaseURL
}

func (p *Provider) streamer(simple bool) sdk.ProviderStreamFunc {
	return func(model, request map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		return p.stream(simple, model, request, opts)
	}
}

// headerTimeout bounds the wait for response headers and, after that, between stream lines.
const headerTimeout = 180 * time.Second

func (p *Provider) stream(_ bool, model, request map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	signal := opts.Signal
	if signal == nil {
		signal = context.Background()
	}
	options := opts.Values
	out := sdk.CreateAssistantMessageEventStream()
	st := &streamer{out: out, o: newOutput(model, time.Now()), ids: requiresToolCallID(str(model["id"])), now: time.Now}
	go func() {
		body, secrets, err := p.open(signal, model, request, options, opts)
		if err != nil {
			st.push(map[string]any{"type": "start"})
			if signal.Err() != nil {
				st.fail("aborted", &RPCError{Reason: "ABORTED", Message: "Request was aborted"})
				return
			}
			st.fail("error", err)
			return
		}
		idle := headerTimeout
		if ms, ok := options["timeoutMs"].(float64); ok && ms > 0 {
			idle = time.Duration(ms) * time.Millisecond
		}
		st.run(signal, body, idle, strings.HasPrefix(str(model["id"]), "gpt-oss"), secrets)
	}()
	return out, nil
}

// open sends the request and returns the unwrapped Gemini SSE body.
func (p *Provider) open(signal context.Context, model, request, options map[string]any, opts sdk.ProviderStreamOptions) (io.ReadCloser, []string, error) {
	access, _ := options["apiKey"].(string)
	if access == "" {
		return nil, nil, errors.New("antigravity: no OAuth login; run /login antigravity")
	}
	id := str(model["id"])
	levels, _ := model["thinkingLevelMap"].(map[string]any)
	if levels == nil {
		levels = p.Catalog.LevelMap(id)
	}
	requested, _ := options["reasoning"].(string)
	level, err := PickLevel(levels, requested)
	if err != nil {
		return nil, nil, err
	}
	project, err := p.Projects.Resolve(signal, access)
	if err != nil {
		return nil, nil, err
	}
	secrets := []string{access, project}
	maxTokens := intOf(options["maxTokens"])
	if maxTokens <= 0 {
		maxTokens = intOf(model["maxTokens"])
	}
	key := sessionKey(options, request)
	env := Envelope{
		Model: id, Level: level, Project: project, MaxTokens: maxTokens,
		Meta: func(step int) Metadata { return p.Sessions.Begin(key, id, level.Enum, step) },
	}
	var payload any = env.Wrap(GeminiBody(model, p.Runtime.Ordered(request), "", maxTokens))
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(payload, model)
		if err != nil {
			return nil, secrets, err
		}
		if next != nil {
			payload = next
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, secrets, fmt.Errorf("antigravity: encode request: %w", err)
	}
	timeout := headerTimeout
	if ms, ok := options["timeoutMs"].(float64); ok && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	resp, err := p.post(signal, access, body, timeout)
	if err != nil {
		return nil, secrets, err
	}
	if opts.OnResponse != nil {
		headers := map[string]any{}
		for k := range resp.Header {
			headers[strings.ToLower(k)] = resp.Header.Get(k)
		}
		if err := opts.OnResponse(map[string]any{"status": resp.StatusCode, "headers": headers}, model); err != nil {
			_ = resp.Body.Close()
			return nil, secrets, err
		}
	}
	if resp.StatusCode/100 == 2 {
		return newUnwrapSSE(resp.Body, secrets...), secrets, nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if isMissingSignature(resp.StatusCode, data) {
		if repaired, ok := RepairHistory(body); ok {
			resp, err = p.post(signal, access, repaired, timeout)
			if err != nil {
				return nil, secrets, err
			}
			if resp.StatusCode/100 == 2 {
				return newUnwrapSSE(resp.Body, secrets...), secrets, nil
			}
			data, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
		}
	}
	e := ParseRPCError(data, resp.StatusCode, secrets...)
	if e.RetryAfter == 0 {
		if d, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
			e.RetryAfter = d
		}
	}
	return nil, secrets, e
}

// post sends one envelope; timeout bounds only the wait for response headers.
func (p *Provider) post(signal context.Context, access string, body []byte, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancel(signal)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base()+"/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header = CLIHeaders(access)
	client := p.Client
	if client == nil {
		client = wire.Client
	}
	timer := time.AfterFunc(timeout, cancel)
	resp, err := client.Do(req)
	if !timer.Stop() && err != nil && signal.Err() == nil {
		cancel()
		return nil, &RPCError{Reason: "RESPONSE_TIMEOUT", Message: fmt.Sprintf("No response within %s", timeout)}
	}
	if err != nil {
		cancel()
		return nil, &RPCError{Reason: "REQUEST_FAILED", Message: httpErrorText(err)}
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// httpErrorText drops the request URL from transport errors.
func httpErrorText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func retryAfter(v string) (time.Duration, bool) {
	return httpx.ParseRetryAfter(v, time.Now())
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// sessionKey is the PiG session id, else the first non-system message's timestamp, so
// unrelated conversations never share a trajectory.
func sessionKey(options, request map[string]any) string {
	if s, _ := options["sessionId"].(string); s != "" {
		return s
	}
	messages, _ := request["messages"].([]any)
	for _, raw := range messages {
		m, _ := raw.(map[string]any)
		if m["role"] != "system" && m["timestamp"] != nil {
			return fmt.Sprintf("message:%v", m["timestamp"])
		}
	}
	return "__default__"
}
