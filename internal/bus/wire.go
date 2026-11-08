// Package bus serves the pi-auth-plus v1 event-bus contract: quota and token requests,
// replies and broadcasts.
package bus

import (
	"context"
	"math"
	"sync"
)

// Version is the payload version of every channel.
const Version = 1

// Channel prefixes of the two services.
const (
	QuotaPrefix = "pi-auth-plus:v1:quotas:"
	TokenPrefix = "pi-auth-plus:v1:tokens:"
)

// Transport is the host's shared event bus.
type Transport interface {
	// On subscribes fn to channel and returns an idempotent unsubscribe.
	On(channel string, fn func(data any)) (func(), error)
	// Emit delivers payload to the channel's listeners.
	Emit(channel string, payload any) error
}

// Runner starts session-bound work; it reports false when the session is over.
type Runner func(fn func(ctx context.Context)) bool

// request is a decoded envelope. The payload is nil when it was absent or null.
type request struct {
	nonce   string
	payload map[string]any
}

// decode returns false for an envelope that cannot be answered: no nonce to reply to.
func decode(data any) (request, bool) {
	env, ok := data.(map[string]any)
	if !ok {
		return request{}, false
	}
	nonce, ok := env["nonce"].(string)
	if !ok || nonce == "" {
		return request{}, false
	}
	req := request{nonce: nonce}
	switch p := env["payload"].(type) {
	case nil:
	case map[string]any:
		req.payload = p
	default:
		return request{}, false
	}
	return req, true
}

// optionalStrings reads an optional string array; null is absence.
func optionalStrings(p map[string]any, key string) (names []string, present bool, ok bool) {
	raw, exists := p[key]
	if !exists || raw == nil {
		return nil, false, true
	}
	list, isList := raw.([]any)
	if !isList {
		return nil, false, false
	}
	names = make([]string, 0, len(list))
	for _, v := range list {
		s, isString := v.(string)
		if !isString {
			return nil, false, false
		}
		names = append(names, s)
	}
	return names, true, true
}

// optionalNumber reads an optional finite, non-negative number; null is absence.
func optionalNumber(p map[string]any, key string) (n float64, present bool, ok bool) {
	raw, exists := p[key]
	if !exists || raw == nil {
		return 0, false, true
	}
	f, isNumber := raw.(float64)
	if !isNumber || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false, false
	}
	return f, true, true
}

// optionalBool reads an optional boolean; null is absence.
func optionalBool(p map[string]any, key string) (b bool, ok bool) {
	raw, exists := p[key]
	if !exists || raw == nil {
		return false, true
	}
	b, ok = raw.(bool)
	return b, ok
}

// optionalString reads an optional string; null is absence.
func optionalString(p map[string]any, key string) (s string, ok bool) {
	raw, exists := p[key]
	if !exists || raw == nil {
		return "", true
	}
	s, ok = raw.(string)
	return s, ok
}

type replyEnvelope struct {
	Nonce   string `json:"nonce"`
	Payload any    `json:"payload"`
}

type okReply struct {
	V  int  `json:"v"`
	OK bool `json:"ok"`
}

type failReply struct {
	V     int    `json:"v"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// replier sends nonce-correlated replies while the session lives.
type replier struct {
	t    Transport
	ctx  context.Context
	warn func(string)
}

func (r replier) send(channel, nonce string, payload any) {
	if r.ctx.Err() != nil {
		return
	}
	if err := r.t.Emit(channel, replyEnvelope{Nonce: nonce, Payload: payload}); err != nil && r.ctx.Err() == nil && r.warn != nil {
		r.warn("pig-auth-plus bus: reply on " + channel + " failed: " + err.Error())
	}
}

func (r replier) fail(channel, nonce, message string) {
	r.send(channel, nonce, failReply{V: Version, OK: false, Error: message})
}

// outbox emits broadcasts in order from one goroutine, so a slow listener never blocks the
// engine or tracker that produced the change.
type outbox struct {
	t    Transport
	warn func(string)

	mu     sync.Mutex
	queue  []message
	wake   chan struct{}
	closed bool
}

type message struct {
	channel string
	payload any
}

// maxQueued bounds memory when the host stalls; the oldest broadcast is dropped first.
const maxQueued = 512

func newOutbox(t Transport, warn func(string)) *outbox {
	return &outbox{t: t, warn: warn, wake: make(chan struct{}, 1)}
}

func (o *outbox) post(channel string, payload any) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	if len(o.queue) >= maxQueued {
		o.queue = o.queue[1:]
	}
	o.queue = append(o.queue, message{channel, payload})
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *outbox) run(ctx context.Context) {
	for {
		o.mu.Lock()
		var next *message
		if len(o.queue) > 0 {
			m := o.queue[0]
			next = &m
			o.queue = o.queue[1:]
		}
		o.mu.Unlock()
		if next == nil {
			select {
			case <-o.wake:
				continue
			case <-ctx.Done():
				o.mu.Lock()
				o.closed = true
				o.queue = nil
				o.mu.Unlock()
				return
			}
		}
		if ctx.Err() != nil {
			continue
		}
		if err := o.t.Emit(next.channel, next.payload); err != nil && ctx.Err() == nil && o.warn != nil {
			o.warn("pig-auth-plus bus: broadcast on " + next.channel + " failed: " + err.Error())
		}
	}
}
