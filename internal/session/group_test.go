package session

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestCloseJoinsAndRejectsLateWork(t *testing.T) {
	g := NewGroup()
	var stopped atomic.Bool
	g.Go(func(ctx context.Context) {
		<-ctx.Done()
		stopped.Store(true)
	})
	g.Close()
	if !stopped.Load() {
		t.Fatal("Close returned before the goroutine finished")
	}
	if g.Go(func(context.Context) { t.Error("ran after Close") }) {
		t.Fatal("Go after Close must not start work")
	}
}
