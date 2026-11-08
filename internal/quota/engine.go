package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// Config is the part of the settings the engine reads; it is called on every decision so
// settings changes apply without a restart.
type Config struct {
	// Providers selects sources by id or alias; empty means all. A non-empty list with no
	// recognized source enables none.
	Providers []string
	// Interval is the ordinary endpoint freshness; source floors still apply.
	Interval time.Duration
}

// Options configure an [Engine].
type Options struct {
	Registry  *Registry
	CachePath string
	Config    func() Config
	Now       func() time.Time
	// AuthFor resolves a PiG provider's credential; nil, nil means signed out.
	AuthFor func(ctx context.Context, provider string) (*Auth, error)
	// Offline suppresses automatic network refreshes; explicit refreshes still run.
	Offline func() bool
}

// View is one enabled provider's effective quota state; building it does no I/O.
type View struct {
	Source   *Source
	Snapshot *Snapshot
	Backoff  *Backoff
	// Refreshing is true while a fetch is in flight in this session.
	Refreshing bool
	// FetchedAt is the last endpoint fetch in epoch ms; 0 means none has happened yet.
	FetchedAt int64
	Drainage  []History
}

// RefreshOptions tune one refresh.
type RefreshOptions struct {
	Force  bool
	MinAge time.Duration
}

// Requester names the surface that asked to spend a reset credit.
type Requester string

// Reset requesters.
const (
	FromPanel   Requester = "panel"
	FromCommand Requester = "command"
	FromBus     Requester = "bus"
)

const (
	pollTick    = 5 * time.Second
	failBackoff = 30 * time.Second
)

var backoffPolicy = BackoffPolicy{Default: failBackoff, RateLimit: time.Minute, Max: 15 * time.Minute, RateLimitStatuses: []int{403, 429}}

type flight struct{ done chan struct{} }

// Engine owns endpoint refresh scheduling and the host-shared quota state for one session.
type Engine struct {
	o     Options
	cache *Cache[State]

	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	refreshing map[string]*flight
	listeners  map[int]func()
	resetSubs  map[int]func(ResetOutcome, Requester)
	nextID     int
}

// NewEngine creates a stopped engine; reads work immediately, refreshes need [Engine.Start].
func NewEngine(o Options) *Engine {
	if o.Now == nil {
		o.Now = time.Now
	}
	e := &Engine{o: o, refreshing: map[string]*flight{}, listeners: map[int]func(){}, resetSubs: map[int]func(ResetOutcome, Requester){}}
	e.cache = NewCache(CacheOptions[State]{
		Path: o.CachePath, Now: o.Now, HoldStatuses: backoffPolicy.RateLimitStatuses,
		Parse: func(raw json.RawMessage) (State, bool) { return ParseState(raw, o.Now().UnixMilli()) },
		Version: func(id string) int {
			if s := o.Registry.ByID(id); s != nil {
				return s.ParserVersion
			}
			return 0
		},
	})
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.cancel()
	return e
}

// Registry returns the source registry.
func (e *Engine) Registry() *Registry { return e.o.Registry }

// Sources returns the enabled sources in registration order.
func (e *Engine) Sources() []*Source {
	cfg := e.o.Config()
	if len(cfg.Providers) == 0 {
		return e.o.Registry.All()
	}
	ids := map[string]bool{}
	for _, n := range cfg.Providers {
		if s := e.o.Registry.Resolve(n); s != nil {
			ids[s.ID] = true
		}
	}
	var out []*Source
	for _, s := range e.o.Registry.All() {
		if ids[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func (e *Engine) source(id string) *Source {
	for _, s := range e.Sources() {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// View returns the enabled providers and their effective state; no disk or network work.
func (e *Engine) View() []View {
	srcs := e.Sources()
	out := make([]View, 0, len(srcs))
	e.mu.Lock()
	busy := make(map[string]bool, len(e.refreshing))
	for id := range e.refreshing {
		busy[id] = true
	}
	e.mu.Unlock()
	for _, s := range srcs {
		v := View{Source: s, Refreshing: busy[s.ID]}
		if b, ok := e.cache.Backoff(s.ID); ok {
			v.Backoff = &b
		}
		if entry, ok := e.cache.Get(s.ID); ok {
			v.Snapshot = Effective(entry.Value.Snapshot, entry.Value.Headers)
			v.FetchedAt = entry.FetchedAt
			v.Drainage = entry.Value.Drainage
		}
		out = append(out, v)
	}
	return out
}

// OnChange observes cache merges, refresh activity and failures; call the result to stop.
func (e *Engine) OnChange(fn func()) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextID
	e.nextID++
	e.listeners[id] = fn
	return func() { e.mu.Lock(); delete(e.listeners, id); e.mu.Unlock() }
}

// OnResetConsumed observes only confirmed provider verdicts that spent a credit.
func (e *Engine) OnResetConsumed(fn func(ResetOutcome, Requester)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextID
	e.nextID++
	e.resetSubs[id] = fn
	return func() { e.mu.Lock(); delete(e.resetSubs, id); e.mu.Unlock() }
}

func (e *Engine) notify(ctx context.Context) {
	e.mu.Lock()
	fns := make([]func(), 0, len(e.listeners))
	for _, fn := range e.listeners {
		fns = append(fns, fn)
	}
	e.mu.Unlock()
	for _, fn := range fns {
		if ctx.Err() != nil {
			return
		}
		fn()
	}
}

// Start begins a session: sync, initial background refreshes and the poll timer.
func (e *Engine) Start(parent context.Context) error {
	e.Stop()
	e.mu.Lock()
	e.ctx, e.cancel = context.WithCancel(parent)
	ctx := e.ctx
	e.mu.Unlock()
	if _, err := e.cache.Sync(ctx, false); err != nil && ctx.Err() == nil {
		e.notify(ctx)
	}
	if !e.offline() {
		for _, s := range e.Sources() {
			e.background(ctx, s.ID, RefreshOptions{})
		}
	}
	e.spawn(ctx, func() {
		t := time.NewTicker(pollTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.Tick(ctx)
			}
		}
	})
	return nil
}

// Stop cancels session-owned work and waits for it; late results cannot update the cache.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.cancel()
	e.mu.Unlock()
	e.wg.Wait()
	e.mu.Lock()
	clear(e.refreshing)
	e.mu.Unlock()
}

func (e *Engine) offline() bool { return e.o.Offline != nil && e.o.Offline() }

func (e *Engine) minAge(s *Source, requested time.Duration) time.Duration {
	if requested == 0 {
		requested = e.o.Config().Interval
	}
	return max(requested, s.MinPoll)
}

// Tick syncs other sessions' writes, then refreshes providers that are due.
func (e *Engine) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if changed, _ := e.cache.Sync(ctx, false); changed {
		e.notify(ctx)
	}
	if e.offline() {
		return
	}
	for _, s := range e.Sources() {
		if _, held := e.cache.Backoff(s.ID); held {
			continue
		}
		e.mu.Lock()
		_, busy := e.refreshing[s.ID]
		e.mu.Unlock()
		if busy {
			continue
		}
		if entry, ok := e.cache.Get(s.ID); ok && e.o.Now().UnixMilli()-entry.FetchedAt < e.minAge(s, 0).Milliseconds() {
			continue
		}
		e.background(ctx, s.ID, RefreshOptions{})
	}
}

func (e *Engine) background(ctx context.Context, id string, o RefreshOptions) {
	e.spawn(ctx, func() { _ = e.Refresh(ctx, id, o) })
}

// spawn starts session-owned work unless the session has stopped. Adding under the mutex
// that Stop cancels under keeps the wait group's add-before-wait rule.
func (e *Engine) spawn(ctx context.Context, fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() == nil {
		e.wg.Go(fn)
	}
}

// Refresh refreshes an enabled provider and waits for it. Calls for a provider already being
// refreshed in this session share that refresh. ctx only bounds the wait.
func (e *Engine) Refresh(ctx context.Context, id string, o RefreshOptions) error {
	src := e.source(id)
	e.mu.Lock()
	run := e.ctx
	if src == nil || run.Err() != nil {
		e.mu.Unlock()
		return nil
	}
	if f, ok := e.refreshing[id]; ok {
		e.mu.Unlock()
		return wait(ctx, f.done)
	}
	f := &flight{done: make(chan struct{})}
	e.refreshing[id] = f
	e.wg.Go(func() {
		e.runRefresh(run, src, o)
		e.mu.Lock()
		if e.refreshing[id] == f {
			delete(e.refreshing, id)
		}
		e.mu.Unlock()
		close(f.done)
		e.notify(run)
	})
	e.mu.Unlock()
	e.notify(run)
	return wait(ctx, f.done)
}

func wait(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) env(ctx context.Context, s *Source) Env {
	return Env{Now: e.o.Now, Auth: func(ctx context.Context) (*Auth, error) {
		lookup := func(p string) (*Auth, error) { return e.o.AuthFor(ctx, p) }
		if s.AuthFrom != nil {
			return s.AuthFrom(ctx, lookup)
		}
		return lookup(s.ID)
	}}
}

func (e *Engine) runRefresh(ctx context.Context, s *Source, o RefreshOptions) {
	ok, err := e.cache.TryAcquire(ctx, s.ID, AcquireOptions{MinAge: e.minAge(s, o.MinAge), Force: o.Force})
	if err != nil || !ok || ctx.Err() != nil {
		return
	}
	snap, err := s.Fetch(ctx, e.env(ctx, s))
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		var status *httpx.StatusError
		code, retry := 0, time.Duration(0)
		if errors.As(err, &status) {
			code, retry = status.Status, status.RetryAfter
		}
		_ = e.cache.Fail(ctx, s.ID, backoffPolicy.For(code, retry), code, err.Error())
		return
	}
	now := e.o.Now().UnixMilli()
	_ = e.cache.PutWith(ctx, s.ID, func(cur State, _ bool) State {
		next := State{Snapshot: &snap, Drainage: Record(cur.Drainage, snap, now)}
		// Headers observed while this fetch was in flight are newer than its snapshot.
		if cur.Headers != nil && cur.Headers.ObservedAt > snap.FetchedAt {
			next.Headers = cur.Headers
		}
		return next
	})
}

// IngestHeaders stores the first enabled parser's claim without refreshing endpoint
// freshness. With strict, only the hinted provider is considered. It returns the provider
// id that took the observation, or "".
func (e *Engine) IngestHeaders(ctx context.Context, headers map[string]string, hint string, strict bool) string {
	run := e.currentCtx()
	if run.Err() != nil {
		return ""
	}
	norm := make(map[string]string, len(headers))
	for k, v := range headers {
		norm[strings.ToLower(k)] = v
	}
	srcs := e.Sources()
	if h := e.o.Registry.Resolve(hint); hint != "" && h != nil {
		i := slices.IndexFunc(srcs, func(s *Source) bool { return s.ID == h.ID })
		switch {
		case i >= 0:
			srcs = slices.Concat([]*Source{srcs[i]}, srcs[:i], srcs[i+1:])
			if strict {
				srcs = srcs[:1]
			}
		case strict:
			srcs = nil
		}
	} else if strict {
		srcs = nil
	}
	now := e.o.Now()
	for _, s := range srcs {
		if s.Headers == nil {
			continue
		}
		snap := safeHeaders(s, norm, now)
		if snap == nil {
			continue
		}
		snap.Provider = s.ID
		snap.Source = FromHeaders
		ms := now.UnixMilli()
		changed, err := e.cache.Patch(run, s.ID, func(cur State, _ bool) (State, bool) {
			cur.Headers = &HeaderObservation{Snapshot: *snap, ObservedAt: ms}
			cur.Drainage = Record(cur.Drainage, Snapshot{Provider: snap.Provider, FetchedAt: ms, Windows: snap.Windows, Credits: snap.Credits}, ms)
			return cur, true
		})
		if err != nil || !changed || run.Err() != nil {
			return ""
		}
		e.notify(run)
		return s.ID
	}
	return ""
}

// safeHeaders keeps a malformed response from interrupting the host or another parser.
func safeHeaders(s *Source, h map[string]string, now time.Time) (out *Snapshot) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return s.Headers(h, now)
}

func (e *Engine) currentCtx() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctx
}

// ResetSupport reports whether an enabled provider exposes on-demand resets.
func (e *Engine) ResetSupport(id string) bool {
	s := e.source(id)
	return s != nil && s.Resets != nil
}

func (e *Engine) resetSource(id string) (*Source, error) {
	if e.currentCtx().Err() != nil {
		return nil, errors.New("quota engine is stopped; start a session before requesting resets")
	}
	if e.o.Registry.ByID(id) == nil {
		return nil, fmt.Errorf("unknown provider %q; known: %s", id, e.names(e.o.Registry.All(), nil))
	}
	s := e.source(id)
	if s == nil {
		return nil, fmt.Errorf("quota provider %q is disabled; enable it in quotas.providers", id)
	}
	if s.Resets == nil {
		return nil, fmt.Errorf("%s exposes no verified on-demand reset endpoint; supported: %s", id, e.names(e.Sources(), func(s *Source) bool { return s.Resets != nil }))
	}
	return s, nil
}

func (e *Engine) names(srcs []*Source, keep func(*Source) bool) string {
	var ids []string
	for _, s := range srcs {
		if keep == nil || keep(s) {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// ListResets lists credits without consuming or refreshing quotas.
func (e *Engine) ListResets(ctx context.Context, id string) (ResetList, error) {
	s, err := e.resetSource(id)
	if err != nil {
		return ResetList{}, err
	}
	return s.Resets.List(ctx, e.env(ctx, s))
}

// ConsumeReset sends one consume request, never retried, then force-refreshes even when the
// provider reports no spend or the request fails: a failed request may still have spent.
func (e *Engine) ConsumeReset(ctx context.Context, id, creditID string, by Requester) (ResetOutcome, error) {
	s, err := e.resetSource(id)
	if err != nil {
		return ResetOutcome{}, err
	}
	run := e.currentCtx()
	outcome, err := s.Resets.Consume(ctx, e.env(ctx, s), creditID)
	if err == nil && outcome.Code == ResetDone {
		e.mu.Lock()
		subs := make([]func(ResetOutcome, Requester), 0, len(e.resetSubs))
		for _, fn := range e.resetSubs {
			subs = append(subs, fn)
		}
		e.mu.Unlock()
		for _, fn := range subs {
			if run.Err() != nil {
				break
			}
			fn(outcome, by)
		}
	}
	// A storage failure here must not hide a spend verdict or invite a consume retry.
	if run.Err() == nil {
		_ = e.Refresh(run, id, RefreshOptions{Force: true})
	}
	return outcome, err
}
