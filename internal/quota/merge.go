package quota

// HeaderObservation is a snapshot parsed from inference response headers.
type HeaderObservation struct {
	Snapshot   Snapshot `json:"snapshot"`
	ObservedAt int64    `json:"observedAt"`
}

// Effective overlays newer header observations on the endpoint snapshot without touching
// endpoint freshness. Unknown header values never erase known endpoint values.
func Effective(endpoint *Snapshot, headers *HeaderObservation) *Snapshot {
	if endpoint == nil {
		if headers == nil {
			return nil
		}
		s := headers.Snapshot
		return &s
	}
	if headers == nil || headers.ObservedAt <= endpoint.FetchedAt {
		return endpoint
	}
	out := *endpoint
	out.Windows = append([]Window(nil), endpoint.Windows...)
	index := map[string]int{}
	for i, w := range out.Windows {
		index[w.ID] = i
	}
	for _, in := range headers.Snapshot.Windows {
		i, ok := index[in.ID]
		if !ok {
			index[in.ID] = len(out.Windows)
			out.Windows = append(out.Windows, in)
			continue
		}
		w := out.Windows[i]
		if in.Used != nil {
			w.Used, w.Value = in.Used, in.Value
		}
		if in.ResetAtMs != nil {
			w.ResetAtMs = in.ResetAtMs
		}
		out.Windows[i] = w
	}
	if headers.Snapshot.Tier != "" {
		out.Tier = headers.Snapshot.Tier
	}
	if headers.Snapshot.Credits != nil {
		out.Credits = headers.Snapshot.Credits
	}
	if headers.Snapshot.ResetCredits != nil {
		out.ResetCredits = headers.Snapshot.ResetCredits
	}
	return &out
}
