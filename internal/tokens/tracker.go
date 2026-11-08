package tokens

import (
	"context"
	"sync"
	"time"
)

// Reason says what made the tracker recount.
type Reason string

// Tracker reasons: the session events that can change the context.
const (
	ReasonSessionStart        Reason = "session_start"
	ReasonModelSelect         Reason = "model_select"
	ReasonThinkingLevelSelect Reason = "thinking_level_select"
	ReasonMessageEnd          Reason = "message_end"
	ReasonTurnEnd             Reason = "turn_end"
	ReasonSessionCompact      Reason = "session_compact"
	ReasonSessionTree         Reason = "session_tree"
	ReasonRequest             Reason = "request"
	ReasonSessionShutdown     Reason = "session_shutdown"
)

// Key identifies what a report describes; a different key makes the previous report stale.
type Key struct {
	Provider, Model string
	// Branch is the session id and leaf id; empty when not known for the event.
	Branch string
}

// DefaultDebounce coalesces bursts of events into one recount.
const DefaultDebounce = 250 * time.Millisecond

// Tracker owns the latest report and the cancellable recount behind it.
type Tracker struct {
	engine   *Engine
	gather   func(ctx context.Context) (Input, bool)
	debounce time.Duration

	mu        sync.Mutex
	latest    *Report
	key       Key
	timer     *time.Timer
	cancel    context.CancelFunc
	listeners map[int]func(*Report, Reason)
	nextID    int
	closed    bool
}

// NewTracker counts with engine using the live state gather returns; gather reports false when
// there is no model to count for.
func NewTracker(engine *Engine, gather func(ctx context.Context) (Input, bool), debounce time.Duration) *Tracker {
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	return &Tracker{engine: engine, gather: gather, debounce: debounce, listeners: map[int]func(*Report, Reason){}}
}

// Latest is the most recent report, nil while unknown.
func (t *Tracker) Latest() *Report {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.latest
}

// OnChange registers fn for every new or cleared report and returns its unsubscribe function.
// fn runs without the tracker's lock held.
func (t *Tracker) OnChange(fn func(*Report, Reason)) (unsubscribe func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.nextID
	t.nextID++
	t.listeners[id] = fn
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.listeners, id)
	}
}

// Schedule debounces a recount for reason. A new session, a thinking-level change, a different
// model or a different branch clears the report immediately: it no longer describes the context.
func (t *Tracker) Schedule(reason Reason, key Key) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.stopLocked()
	invalidate := reason == ReasonSessionStart || reason == ReasonThinkingLevelSelect ||
		key.Provider != t.key.Provider || key.Model != t.key.Model ||
		(reason == ReasonSessionTree && key.Branch != t.key.Branch)
	if key.Branch == "" {
		key.Branch = t.key.Branch
	}
	t.key = key
	cleared := invalidate && t.latest != nil
	if cleared {
		t.latest = nil
	}
	listeners := t.snapshotLocked()
	t.timer = time.AfterFunc(t.debounce, func() { t.Refresh(context.Background(), reason) })
	t.mu.Unlock()
	if cleared {
		notify(listeners, nil, reason)
	}
}

// Refresh recounts now and returns the report; it reports false when superseded, cancelled or
// when there is no model.
func (t *Tracker) Refresh(ctx context.Context, reason Reason) (Report, bool) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return Report{}, false
	}
	t.stopLocked()
	run, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.mu.Unlock()
	defer cancel()

	in, ok := t.gather(run)
	var report Report
	if ok {
		report, ok = t.engine.Context(run, in)
	}
	t.mu.Lock()
	if t.closed || run.Err() != nil {
		t.mu.Unlock()
		return Report{}, false
	}
	t.cancel = nil
	var next *Report
	if ok {
		next = &report
	}
	t.latest = next
	listeners := t.snapshotLocked()
	t.mu.Unlock()
	notify(listeners, next, reason)
	return report, ok
}

// Breakdown recounts the full request by contribution.
func (t *Tracker) Breakdown(ctx context.Context) ([]Part, bool) {
	in, ok := t.gather(ctx)
	if !ok {
		return nil, false
	}
	return t.engine.Breakdown(ctx, in)
}

// Close cancels pending work and clears the report.
func (t *Tracker) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.stopLocked()
	had := t.latest != nil
	t.latest = nil
	listeners := t.snapshotLocked()
	t.listeners = map[int]func(*Report, Reason){}
	t.mu.Unlock()
	if had {
		notify(listeners, nil, ReasonSessionShutdown)
	}
}

func (t *Tracker) stopLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

func (t *Tracker) snapshotLocked() []func(*Report, Reason) {
	out := make([]func(*Report, Reason), 0, len(t.listeners))
	for _, fn := range t.listeners {
		out = append(out, fn)
	}
	return out
}

func notify(listeners []func(*Report, Reason), r *Report, reason Reason) {
	for _, fn := range listeners {
		fn(r, reason)
	}
}
