package tokens

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func newTestTracker(in *atomic.Value, debounce time.Duration) *Tracker {
	engine := &Engine{For: func(string, string) Counter { return EstimateCounter{Ratios: DefaultRatios} }}
	return NewTracker(engine, func(context.Context) (Input, bool) {
		v := in.Load().(Input)
		return v, v.Model != ""
	}, debounce)
}

func input(model, text string) Input {
	return Input{Provider: "p", Model: model, LiveSystem: text, Messages: []map[string]any{{"role": "user", "content": "hello"}}}
}

func TestTrackerDebouncesAndNotifies(t *testing.T) {
	var in atomic.Value
	in.Store(input("m", "sys"))
	tr := newTestTracker(&in, 10*time.Millisecond)
	defer tr.Close()
	got := make(chan *Report, 4)
	tr.OnChange(func(r *Report, _ Reason) { got <- r })
	for range 5 {
		tr.Schedule(ReasonMessageEnd, Key{Provider: "p", Model: "m"})
	}
	select {
	case r := <-got:
		if r == nil || r.Model != "m" {
			t.Fatalf("%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no report")
	}
	select {
	case <-got:
		t.Fatal("a burst must produce one recount")
	case <-time.After(60 * time.Millisecond):
	}
}

func TestTrackerClearsReportWhenModelChanges(t *testing.T) {
	var in atomic.Value
	in.Store(input("m", "sys"))
	tr := newTestTracker(&in, time.Hour)
	defer tr.Close()
	if _, ok := tr.Refresh(context.Background(), ReasonRequest); !ok || tr.Latest() == nil {
		t.Fatal("expected a report")
	}
	tr.Schedule(ReasonModelSelect, Key{Provider: "p", Model: "other"})
	if tr.Latest() != nil {
		t.Fatal("a report for another model must be cleared immediately")
	}
}

func TestTrackerKeepsReportOnSameModelMessageEnd(t *testing.T) {
	var in atomic.Value
	in.Store(input("m", "sys"))
	tr := newTestTracker(&in, time.Hour)
	defer tr.Close()
	tr.Schedule(ReasonSessionStart, Key{Provider: "p", Model: "m", Branch: "s/1"})
	tr.Refresh(context.Background(), ReasonRequest)
	tr.Schedule(ReasonMessageEnd, Key{Provider: "p", Model: "m"})
	if tr.Latest() == nil {
		t.Fatal("same model and branch keeps its report until the recount lands")
	}
	tr.Schedule(ReasonSessionTree, Key{Provider: "p", Model: "m", Branch: "s/2"})
	if tr.Latest() != nil {
		t.Fatal("a different branch clears the report")
	}
}

func TestTrackerDropsResultOfSupersededRefresh(t *testing.T) {
	release := make(chan struct{})
	engine := &Engine{For: func(string, string) Counter { return EstimateCounter{Ratios: DefaultRatios} }}
	var calls atomic.Int32
	tr := NewTracker(engine, func(ctx context.Context) (Input, bool) {
		if calls.Add(1) == 1 {
			<-release
		}
		return input("m", "sys"), true
	}, time.Hour)
	defer tr.Close()
	done := make(chan bool)
	go func() { _, ok := tr.Refresh(context.Background(), ReasonRequest); done <- ok }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, ok := tr.Refresh(context.Background(), ReasonRequest); !ok {
		t.Fatal("latest refresh must succeed")
	}
	close(release)
	if <-done {
		t.Fatal("the superseded refresh must report false")
	}
}

func TestTrackerCloseStopsWork(t *testing.T) {
	var in atomic.Value
	in.Store(input("m", "sys"))
	tr := newTestTracker(&in, 5*time.Millisecond)
	got := make(chan *Report, 1)
	tr.OnChange(func(r *Report, _ Reason) { got <- r })
	tr.Schedule(ReasonMessageEnd, Key{Provider: "p", Model: "m"})
	tr.Close()
	select {
	case r := <-got:
		t.Fatalf("closed tracker reported %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
}
