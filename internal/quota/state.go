package quota

import (
	"encoding/json"
	"math"
)

// State is everything cached for one provider.
type State struct {
	Snapshot *Snapshot          `json:"snapshot,omitempty"`
	Headers  *HeaderObservation `json:"headers,omitempty"`
	Drainage []History          `json:"drainage,omitempty"`
}

// ParseState validates a cached state at the read boundary: malformed fields are dropped,
// valid unknown-valued windows and balances are kept.
func ParseState(raw json.RawMessage, now int64) (State, bool) {
	var in State
	if json.Unmarshal(raw, &in) != nil {
		return State{}, false
	}
	out := State{Drainage: SanitizeHistory(in.Drainage, now)}
	if in.Snapshot != nil && in.Snapshot.Source == FromEndpoint && in.Snapshot.Provider != "" {
		s := sanitizeSnapshot(*in.Snapshot)
		out.Snapshot = &s
	}
	if in.Headers != nil && in.Headers.Snapshot.Source == FromHeaders && in.Headers.Snapshot.Provider != "" {
		h := *in.Headers
		h.Snapshot = sanitizeSnapshot(h.Snapshot)
		out.Headers = &h
	}
	return out, true
}

func sanitizeSnapshot(s Snapshot) Snapshot {
	var ws []Window
	for _, w := range s.Windows {
		if w.ID == "" {
			continue
		}
		if w.Used != nil && (math.IsNaN(*w.Used) || *w.Used < 0 || *w.Used > 1) {
			w.Used = nil
		}
		ws = append(ws, w)
	}
	s.Windows = ws
	return s
}
