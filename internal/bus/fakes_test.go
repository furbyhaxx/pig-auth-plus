package bus

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/session"
)

type emitted struct {
	channel string
	payload map[string]any
}

// fakeTransport records emits as decoded JSON and delivers to subscribed handlers.
type fakeTransport struct {
	mu       sync.Mutex
	handlers map[string][]func(any)
	log      []emitted
	changed  chan struct{}
}

func newTransport() *fakeTransport {
	return &fakeTransport{handlers: map[string][]func(any){}, changed: make(chan struct{}, 256)}
}

func (t *fakeTransport) On(channel string, fn func(any)) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handlers[channel] = append(t.handlers[channel], fn)
	return func() {}, nil
}

func (t *fakeTransport) Emit(channel string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	t.mu.Lock()
	t.log = append(t.log, emitted{channel, decoded})
	t.mu.Unlock()
	t.changed <- struct{}{}
	return nil
}

// deliver sends data as another extension would, in its JSON-decoded form.
func (t *fakeTransport) deliver(channel string, data any) {
	raw, _ := json.Marshal(data)
	var v any
	_ = json.Unmarshal(raw, &v)
	t.mu.Lock()
	hs := append([]func(any){}, t.handlers[channel]...)
	t.mu.Unlock()
	for _, h := range hs {
		h(v)
	}
}

func (t *fakeTransport) on(channel string) []emitted {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []emitted
	for _, e := range t.log {
		if e.channel == channel {
			out = append(out, e)
		}
	}
	return out
}

// await waits until n messages exist on channel.
func (t *fakeTransport) await(tb testing.TB, channel string, n int) []emitted {
	tb.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if got := t.on(channel); len(got) >= n {
			return got
		}
		select {
		case <-t.changed:
		case <-deadline:
			tb.Fatalf("timed out waiting for %d message(s) on %s; have %d", n, channel, len(t.on(channel)))
		}
	}
}

// settle waits briefly so a message that must not arrive has had its chance.
func (t *fakeTransport) settle() { time.Sleep(60 * time.Millisecond) }

type fakeEngine struct {
	mu        sync.Mutex
	reg       *quota.Registry
	enabled   []string
	views     map[string]quota.View
	listeners []func()
	consumes  []string
	consumed  []func(quota.ResetOutcome, quota.Requester)
	refreshes []quota.RefreshOptions
	list      quota.ResetList
	outcome   quota.ResetOutcome
}

func newEngine(sources ...*quota.Source) *fakeEngine {
	e := &fakeEngine{reg: quota.NewRegistry(sources...), views: map[string]quota.View{}}
	for _, s := range sources {
		e.enabled = append(e.enabled, s.ID)
		e.views[s.ID] = quota.View{Source: s}
	}
	return e
}

func (e *fakeEngine) Sources() []*quota.Source {
	var out []*quota.Source
	for _, s := range e.reg.All() {
		for _, id := range e.enabled {
			if id == s.ID {
				out = append(out, s)
			}
		}
	}
	return out
}
func (e *fakeEngine) Registry() *quota.Registry { return e.reg }
func (e *fakeEngine) View() []quota.View {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []quota.View
	for _, s := range e.Sources() {
		out = append(out, e.views[s.ID])
	}
	return out
}
func (e *fakeEngine) Refresh(_ context.Context, _ string, o quota.RefreshOptions) error {
	e.mu.Lock()
	e.refreshes = append(e.refreshes, o)
	e.mu.Unlock()
	return nil
}
func (e *fakeEngine) ListResets(context.Context, string) (quota.ResetList, error) { return e.list, nil }
func (e *fakeEngine) ConsumeReset(_ context.Context, id, creditID string, by quota.Requester) (quota.ResetOutcome, error) {
	e.mu.Lock()
	e.consumes = append(e.consumes, id+"/"+creditID)
	subs := append([]func(quota.ResetOutcome, quota.Requester){}, e.consumed...)
	e.mu.Unlock()
	if e.outcome.Code == quota.ResetDone {
		for _, fn := range subs {
			fn(e.outcome, by)
		}
	}
	return e.outcome, nil
}
func (e *fakeEngine) OnChange(fn func()) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, fn)
	return func() {}
}
func (e *fakeEngine) OnResetConsumed(fn func(quota.ResetOutcome, quota.Requester)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consumed = append(e.consumed, fn)
	return func() {}
}

// set replaces a provider's view and notifies listeners like the engine does.
func (e *fakeEngine) set(v quota.View) {
	e.mu.Lock()
	e.views[v.Source.ID] = v
	fns := append([]func(){}, e.listeners...)
	e.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (e *fakeEngine) consumeCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.consumes)
}

func newGroup(tb testing.TB) *session.Group {
	g := session.NewGroup()
	tb.Cleanup(g.Close)
	return g
}
