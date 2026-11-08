package antigravity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// ProjectError is a failed project discovery, safe to show.
type ProjectError struct {
	Operation string
	Status    int
}

func (e *ProjectError) Error() string {
	s := "antigravity: loadCodeAssist " + e.Operation
	if e.Status != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	return s + "; sign in to Antigravity once, then run /login antigravity again"
}

// Projects discovers the Cloud Code project once per credential. The CLI re-derives it per
// run, so a session resets it at start.
type Projects struct {
	BaseURL string

	mu    sync.Mutex
	cache map[[32]byte]*discovery
}

type discovery struct {
	done    chan struct{}
	project string
	err     error
}

// Reset forgets every discovery.
func (p *Projects) Reset() {
	p.mu.Lock()
	p.cache = nil
	p.mu.Unlock()
}

// Resolve returns the project for access, sharing a concurrent discovery. A failed discovery
// is not cached.
func (p *Projects) Resolve(ctx context.Context, access string) (string, error) {
	key := sha256.Sum256([]byte(access))
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[[32]byte]*discovery{}
	}
	d, ok := p.cache[key]
	if !ok {
		d = &discovery{done: make(chan struct{})}
		p.cache[key] = d
		go func() {
			// Detached from one caller's cancellation so a shared discovery survives it.
			d.project, d.err = p.discover(context.WithoutCancel(ctx), access)
			if d.err != nil {
				p.mu.Lock()
				if p.cache[key] == d {
					delete(p.cache, key)
				}
				p.mu.Unlock()
			}
			close(d.done)
		}()
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-d.done:
		return d.project, d.err
	}
}

func (p *Projects) discover(ctx context.Context, access string) (string, error) {
	base := p.BaseURL
	if base == "" {
		base = BaseURL
	}
	var out struct {
		Project string `json:"cloudaicompanionProject"`
	}
	_, err := httpx.JSON(ctx, httpx.Request{
		Provider: ID, Method: http.MethodPost, URL: base + "/v1internal:loadCodeAssist",
		Header: CLIHeaders(access), Body: []byte(`{"metadata":{"ideType":"ANTIGRAVITY"}}`), Secrets: []string{access},
	}, &out)
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) {
			return "", &ProjectError{Operation: "discovery failed", Status: se.Status}
		}
		return "", &ProjectError{Operation: "discovery failed"}
	}
	if strings.TrimSpace(out.Project) == "" {
		return "", &ProjectError{Operation: "returned no project"}
	}
	return out.Project, nil
}
