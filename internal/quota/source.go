package quota

import (
	"context"
	"strings"
	"time"
)

// Auth is the credential a quota fetch authenticates with, resolved through PiG so OAuth
// refresh stays PiG's.
type Auth struct {
	Token string
}

// Env is what the engine hands a source's fetch.
type Env struct {
	// Auth returns nil when the user has no credential for the source.
	Auth func(ctx context.Context) (*Auth, error)
	Now  func() time.Time
}

// Source is one provider's quota part.
type Source struct {
	// ID is the PiG provider id used in settings, reports and integrations.
	ID    string
	Label string
	// Short is the compact id for narrow lines, e.g. "cl". It is not an accepted alias.
	Short   string
	Aliases []string
	// Credits sources report a balance; others report windows.
	Credits bool
	// ParserVersion invalidates cached snapshots when a parser change makes them wrong.
	ParserVersion int
	// MinPoll is the endpoint floor independent of the configured interval.
	MinPoll time.Duration
	// Fetch returns *httpx.StatusError for HTTP failures so the engine can back off.
	Fetch func(ctx context.Context, env Env) (Snapshot, error)
	// Headers parses inference response headers (keys lower-cased) into a snapshot; nil
	// when the header set is not this provider's shape.
	Headers func(h map[string]string, now time.Time) *Snapshot
	// Resets is optional on-demand reset support; consuming spends a credit.
	Resets Resets
	// AuthFrom resolves credentials that do not live under the source's own PiG id. lookup
	// reads any PiG provider's auth. Without it the engine looks up the source's own id.
	AuthFrom func(ctx context.Context, lookup func(provider string) (*Auth, error)) (*Auth, error)
}

// Resets lists and spends on-demand rate-limit reset credits.
type Resets interface {
	List(ctx context.Context, env Env) (ResetList, error)
	Consume(ctx context.Context, env Env, creditID string) (ResetOutcome, error)
}

// Registry is an ordered set of sources with alias resolution.
type Registry struct{ sources []*Source }

// NewRegistry keeps the sources in the given order, which is the display order.
func NewRegistry(sources ...*Source) *Registry { return &Registry{sources: sources} }

// All returns the sources in registration order.
func (r *Registry) All() []*Source { return r.sources }

// ByID returns the source with the exact PiG id.
func (r *Registry) ByID(id string) *Source {
	for _, s := range r.sources {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Resolve accepts a PiG id, an alias or an unambiguous label, case-insensitively.
func (r *Registry) Resolve(name string) *Source {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return nil
	}
	for _, s := range r.sources {
		if strings.ToLower(s.ID) == n {
			return s
		}
		for _, a := range s.Aliases {
			if strings.ToLower(a) == n {
				return s
			}
		}
	}
	var found *Source
	for _, s := range r.sources {
		if strings.ToLower(s.Label) == n {
			if found != nil {
				return nil
			}
			found = s
		}
	}
	return found
}
