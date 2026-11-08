package quota

import (
	"math"
	"slices"
	"strings"
	"time"
)

// Kind says what a drainage history measures.
type Kind string

// History kinds: a window's used fraction rises; a balance's remaining amount falls.
const (
	KindWindow  Kind = "window"
	KindCredits Kind = "credits"
)

// Observation is one measured value; for a window it is the used fraction, for credits the
// remaining amount.
type Observation struct {
	At        int64   `json:"at"`
	Value     float64 `json:"value"`
	ResetAtMs *int64  `json:"resetAtMs,omitempty"`
}

// History holds the observations of one window or balance within one monotonic segment.
type History struct {
	Kind    Kind          `json:"kind"`
	ID      string        `json:"id"`
	Samples []Observation `json:"samples"`
}

// Rate is a net consumption rate and the sample coverage it was computed over.
type Rate struct {
	PerHour     float64
	CoverageSec float64
}

// Metrics are the drainage rates and exhaustion estimate for the latest observation.
type Metrics struct {
	ObservedAt int64
	Window     *Rate
	H1         *Rate
	H24        *Rate
	// ETASec is the time to exhaustion at the observed rate; nil when unknown.
	ETASec *float64
	// ResetsBeforeExhaustion marks a reset that arrives before the ETA.
	ResetsBeforeExhaustion bool
}

const (
	hourMs = int64(time.Hour / time.Millisecond)
	dayMs  = 24 * hourMs
	// resetJumpMs is how far a reset time may advance before it starts a new segment.
	resetJumpMs = 60_000
	// minCoverageSec is the shortest span a rate is trusted over.
	minCoverageSec = 60
	maxSamples     = 1500
)

func (h History) key() string { return string(h.Kind) + "\x00" + h.ID }

// SanitizeHistory drops malformed or implausibly future observations from decoded histories.
func SanitizeHistory(in []History, now int64) []History {
	var out []History
	for _, h := range in {
		if (h.Kind != KindWindow && h.Kind != KindCredits) || h.ID == "" {
			continue
		}
		kept := h.Samples[:0:0]
		for _, s := range h.Samples {
			if s.At <= 0 || s.At > now+60_000 || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
				continue
			}
			if h.Kind == KindWindow && (s.Value < 0 || s.Value > 1) {
				continue
			}
			kept = append(kept, s)
		}
		out = append(out, History{Kind: h.Kind, ID: h.ID, Samples: kept})
	}
	return out
}

// MergeHistory merges histories, keeping the latest monotonic segment and bounded recent
// observations; a top-up, utilization drop or reset-time advance starts a new segment.
func MergeHistory(left, right []History, now int64) []History {
	merged := map[string]*History{}
	var order []string
	for _, h := range slices.Concat(left, right) {
		k := h.key()
		if m, ok := merged[k]; ok {
			m.Samples = append(m.Samples, h.Samples...)
			continue
		}
		c := History{Kind: h.Kind, ID: h.ID, Samples: slices.Clone(h.Samples)}
		merged[k] = &c
		order = append(order, k)
	}
	var out []History
	for _, k := range order {
		h := merged[k]
		slices.SortFunc(h.Samples, func(a, b Observation) int {
			if a.At != b.At {
				return cmpInt64(a.At, b.At)
			}
			if a.Value != b.Value {
				if a.Value < b.Value {
					return -1
				}
				return 1
			}
			return cmpInt64(deref(a.ResetAtMs), deref(b.ResetAtMs))
		})
		var segment []Observation
		var prev *Observation
		var resetAt *int64
		for i := range h.Samples {
			s := h.Samples[i]
			if prev != nil && prev.At == s.At {
				continue
			}
			reversed := prev != nil && ((h.Kind == KindWindow && s.Value < prev.Value) || (h.Kind == KindCredits && s.Value > prev.Value))
			advanced := resetAt != nil && s.ResetAtMs != nil && *s.ResetAtMs-*resetAt > resetJumpMs
			if reversed || advanced {
				segment = nil
			}
			if s.ResetAtMs != nil {
				resetAt = s.ResetAtMs
			}
			segment = append(segment, s)
			prev = &segment[len(segment)-1]
		}
		if len(segment) == 0 || prev.At < now-7*dayMs {
			continue
		}
		recent := segment[1:]
		cut := prev.At - dayMs
		recent = slices.DeleteFunc(slices.Clone(recent), func(s Observation) bool { return s.At < cut })
		if len(recent) > maxSamples {
			recent = recent[len(recent)-maxSamples:]
		}
		out = append(out, History{Kind: h.Kind, ID: h.ID, Samples: append([]Observation{segment[0]}, recent...)})
	}
	slices.SortFunc(out, func(a, b History) int { return strings.Compare(a.key(), b.key()) })
	return out
}

// Record adds the usable observations of snapshot to history. Failed and signed-out
// snapshots contribute nothing, and an observation not newer than the last one is skipped.
func Record(history []History, s Snapshot, now int64) []History {
	var incoming []History
	if s.Error == "" && !s.NeedsAuth && s.FetchedAt > 0 {
		for _, w := range s.Windows {
			if w.Used == nil || *w.Used < 0 || *w.Used > 1 {
				continue
			}
			incoming = append(incoming, History{Kind: KindWindow, ID: w.ID, Samples: []Observation{{At: s.FetchedAt, Value: *w.Used, ResetAtMs: w.ResetAtMs}}})
		}
		if s.Credits != nil && s.Credits.Remaining != nil {
			incoming = append(incoming, History{Kind: KindCredits, ID: s.Credits.Currency, Samples: []Observation{{At: s.FetchedAt, Value: *s.Credits.Remaining}}})
		}
	}
	var later []History
	for _, in := range incoming {
		i := slices.IndexFunc(history, func(h History) bool { return h.key() == in.key() })
		if i >= 0 && len(history[i].Samples) > 0 && s.FetchedAt <= history[i].Samples[len(history[i].Samples)-1].At {
			continue
		}
		later = append(later, in)
	}
	return MergeHistory(history, later, now)
}

// Compute derives the window, 1 h and 24 h rates and the exhaustion estimate at now.
// Rates need at least 60 s of coverage; a passed reset with no newer observation suppresses
// the ETA but keeps the observed rates.
func Compute(h *History, now int64) *Metrics {
	if h == nil || len(h.Samples) == 0 {
		return nil
	}
	last := h.Samples[len(h.Samples)-1]
	rate := func(lookback int64) *Rate {
		first := -1
		for i, s := range h.Samples {
			if lookback < 0 || s.At >= last.At-lookback {
				first = i
				break
			}
		}
		if first < 0 {
			return nil
		}
		cover := float64(last.At-h.Samples[first].At) / 1000
		if cover < minCoverageSec {
			return nil
		}
		consumed := last.Value - h.Samples[first].Value
		if h.Kind == KindCredits {
			consumed = -consumed
		}
		per := consumed * 3600 / cover
		if math.IsNaN(per) || math.IsInf(per, 0) || per < 0 {
			return nil
		}
		return &Rate{PerHour: per, CoverageSec: cover}
	}
	m := &Metrics{ObservedAt: last.At, Window: rate(-1), H1: rate(hourMs), H24: rate(dayMs)}
	if last.ResetAtMs != nil && *last.ResetAtMs > last.At && *last.ResetAtMs <= now {
		return m
	}
	remaining := last.Value
	if h.Kind == KindWindow {
		remaining = 1 - last.Value
	}
	if remaining > 0 && m.Window != nil && m.Window.PerHour > 0 {
		eta := remaining / m.Window.PerHour * 3600
		if !math.IsInf(eta, 0) && eta > 0 {
			if last.ResetAtMs != nil && *last.ResetAtMs > last.At && float64(*last.ResetAtMs-last.At) < eta*1000 {
				m.ResetsBeforeExhaustion = true
				return m
			}
			m.ETASec = &eta
		}
	}
	return m
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
