// Package session owns per-session lifetimes: background work that must stop and be joined
// when the session ends.
package session

import (
	"context"
	"sync"
)

// Group runs background goroutines bound to one session. The zero value is unusable; use
// [NewGroup].
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewGroup creates a group whose context ends when Close is called.
func NewGroup() *Group {
	ctx, cancel := context.WithCancel(context.Background())
	return &Group{ctx: ctx, cancel: cancel}
}

// Context ends when the group closes.
func (g *Group) Context() context.Context { return g.ctx }

// Go runs fn unless the group is closed; it reports whether fn was started.
func (g *Group) Go(fn func(ctx context.Context)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Go(func() { fn(g.ctx) })
	return true
}

// Close cancels the group's context and waits for every started goroutine.
func (g *Group) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.cancel()
	g.wg.Wait()
}
