// Package tokens counts the model context of a session and labels where each number comes from.
package tokens

import "slices"

// Source says how a count was obtained, from most to least authoritative.
type Source string

// Count sources.
const (
	// SourceProvider is usage a provider reported for a real request.
	SourceProvider Source = "provider"
	// SourceEndpoint is a native count of a complete request; it is not billed usage.
	SourceEndpoint Source = "count-endpoint"
	// SourceEstimate is a measured characters-per-token estimate.
	SourceEstimate Source = "estimate"
)

var precedence = []Source{SourceProvider, SourceEndpoint, SourceEstimate}

// Weaker returns the less authoritative of two sources; a sum is only as good as its weakest part.
func Weaker(a, b Source) Source {
	if slices.Index(precedence, a) >= slices.Index(precedence, b) {
		return a
	}
	return b
}

// Diagnostic is a user-actionable explanation attached to a count that fell back.
type Diagnostic struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

// Count is a token number labelled with its source.
type Count struct {
	Tokens      int          `json:"tokens"`
	Source      Source       `json:"source"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Sum adds counts and labels the result with the weakest source. An empty list is an exact zero.
func Sum(counts ...Count) Count {
	out := Count{Source: SourceProvider}
	for _, c := range counts {
		out.Tokens += c.Tokens
		out.Source = Weaker(out.Source, c.Source)
		for _, d := range c.Diagnostics {
			if !slices.Contains(out.Diagnostics, d) {
				out.Diagnostics = append(out.Diagnostics, d)
			}
		}
	}
	return out
}
