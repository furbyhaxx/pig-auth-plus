// Package config resolves pig-auth-plus settings from built-in defaults, the user file
// and session overrides, and saves edits without losing comments or unknown keys.
package config

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/furbyhaxx/pig-auth-plus/internal/fsx"
)

// Scope is a settings layer, lowest priority first.
type Scope int

// Settings layers.
const (
	ScopeDefault Scope = iota
	ScopeUser
	ScopeSession
)

func (s Scope) String() string {
	return [...]string{"default", "user", "session"}[s]
}

// Snapshot is an immutable resolved view of the settings.
type Snapshot struct {
	Settings
	values   map[string]any
	sources  map[string]Scope
	Warnings []string
}

// Value returns the effective normalized value of key.
func (s *Snapshot) Value(key string) any { return s.values[key] }

// Source returns the layer key resolves from.
func (s *Snapshot) Source(key string) Scope { return s.sources[key] }

// Edit is a staged change; Reset removes the key from the target scope.
type Edit struct {
	Key   string
	Value any
	Reset bool
}

// SaveResult reports what a successful save cannot make effective.
type SaveResult struct {
	// Shadowed lists saved keys that a higher-priority scope still overrides.
	Shadowed []string
}

// Manager owns the layers of one session. It is safe for concurrent use.
type Manager struct {
	path string

	mu       sync.Mutex
	user     map[string]any
	userErr  error
	warnings []string
	session  map[string]any
	snap     *Snapshot
	subs     []func(*Snapshot)
}

// NewManager creates a manager for the user file at path; call [Manager.Load] next.
func NewManager(path string) *Manager {
	m := &Manager{path: path, user: map[string]any{}, session: map[string]any{}}
	m.snap = m.resolve()
	return m
}

// Load reads the user file. A malformed file keeps the last good user values, is reported
// as an error and blocks user saves until it is fixed.
func (m *Manager) Load() error {
	data, err := fsx.ReadOptional(m.path)
	var doc *document
	if err == nil {
		doc, err = parseDocument(m.path, data)
	}
	m.mu.Lock()
	if err != nil {
		m.userErr = err
	} else {
		m.userErr, m.user, m.warnings = nil, doc.values, doc.warnings
	}
	m.snap = m.resolve()
	snap, subs := m.snap, slices.Clone(m.subs)
	m.mu.Unlock()
	notify(subs, snap)
	return err
}

// Snapshot returns the current resolved settings.
func (m *Manager) Snapshot() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

// Subscribe calls fn after every change of the resolved settings.
func (m *Manager) Subscribe(fn func(*Snapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, fn)
}

// Save validates edits and writes them to scope. User saves lock the file, re-read it and
// change only the edited keys, so concurrent saves of other keys are kept.
func (m *Manager) Save(ctx context.Context, scope Scope, edits []Edit) (SaveResult, error) {
	normalized, err := normalizeEdits(edits)
	if err != nil {
		return SaveResult{}, err
	}
	switch scope {
	case ScopeSession:
		return m.saveSession(normalized)
	case ScopeUser:
		return m.saveUser(ctx, normalized)
	}
	return SaveResult{}, fmt.Errorf("cannot save to the %s scope", scope)
}

func normalizeEdits(edits []Edit) ([]edit, error) {
	out := make([]edit, 0, len(edits))
	for _, e := range edits {
		def, ok := Lookup(e.Key)
		if !ok {
			return nil, fmt.Errorf("unknown setting %s", e.Key)
		}
		if e.Reset {
			out = append(out, edit{key: e.Key, reset: true})
			continue
		}
		v, clamped, err := def.Validate(e.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Key, err)
		}
		if clamped {
			return nil, fmt.Errorf("%s must be between %d and %d", e.Key, def.Min, def.Max)
		}
		out = append(out, edit{key: e.Key, value: v})
	}
	return out, nil
}

func (m *Manager) saveSession(edits []edit) (SaveResult, error) {
	m.mu.Lock()
	session := maps.Clone(m.session)
	applyEdits(session, edits)
	if err := checkRelations(m.user, session); err != nil {
		m.mu.Unlock()
		return SaveResult{}, err
	}
	m.session = session
	m.snap = m.resolve()
	snap, subs := m.snap, slices.Clone(m.subs)
	m.mu.Unlock()
	notify(subs, snap)
	return SaveResult{}, nil
}

func (m *Manager) saveUser(ctx context.Context, edits []edit) (SaveResult, error) {
	unlock, err := fsx.Lock(ctx, m.path)
	if err != nil {
		return SaveResult{}, err
	}
	defer unlock()
	data, err := fsx.ReadOptional(m.path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("read %s: %w", m.path, err)
	}
	doc, err := parseDocument(m.path, data)
	if err != nil {
		return SaveResult{}, err
	}
	user := maps.Clone(doc.values)
	applyEdits(user, edits)
	m.mu.Lock()
	session := maps.Clone(m.session)
	m.mu.Unlock()
	if err := checkRelations(user, session); err != nil {
		return SaveResult{}, err
	}
	out, err := doc.apply(edits)
	if err != nil {
		return SaveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return SaveResult{}, err
	}
	if err := fsx.WriteAtomic(m.path, out); err != nil {
		return SaveResult{}, err
	}
	var result SaveResult
	for _, e := range edits {
		if _, ok := session[e.key]; ok {
			result.Shadowed = append(result.Shadowed, e.key)
		}
	}
	m.mu.Lock()
	m.user, m.userErr, m.warnings = user, nil, doc.warnings
	m.snap = m.resolve()
	snap, subs := m.snap, slices.Clone(m.subs)
	m.mu.Unlock()
	notify(subs, snap)
	return result, nil
}

func applyEdits(layer map[string]any, edits []edit) {
	for _, e := range edits {
		if e.reset {
			delete(layer, e.key)
		} else {
			layer[e.key] = e.value
		}
	}
}

// checkRelations rejects a save whose effective values contradict a relation.
func checkRelations(user, session map[string]any) error {
	for _, r := range relations {
		lo, hi := effective(r.Lower, user, session), effective(r.Upper, user, session)
		if lo.(int64) >= hi.(int64) {
			return fmt.Errorf("%s (%d) must be below %s (%d)", r.Lower, lo, r.Upper, hi)
		}
	}
	return nil
}

func effective(key string, layers ...map[string]any) any {
	v := definitions[key].Default
	for _, layer := range layers {
		if x, ok := layer[key]; ok {
			v = x
		}
	}
	return v
}

// resolve layers the values. Loaded values that contradict a relation fall back, for both
// keys, to the highest layer where the pair holds. Callers hold m.mu.
func (m *Manager) resolve() *Snapshot {
	layers := []map[string]any{nil, m.user, m.session}
	values := make(map[string]any, len(Catalog))
	sources := make(map[string]Scope, len(Catalog))
	for _, d := range Catalog {
		values[d.Key], sources[d.Key] = d.Default, ScopeDefault
		for scope := ScopeUser; scope <= ScopeSession; scope++ {
			if v, ok := layers[scope][d.Key]; ok {
				values[d.Key], sources[d.Key] = v, scope
			}
		}
	}
	warnings := slices.Clone(m.warnings)
	if m.userErr != nil {
		warnings = append(warnings, m.userErr.Error())
	}
	for _, r := range relations {
		for top := ScopeSession; ; top-- {
			lo, loSrc := pick(r.Lower, layers, top)
			hi, hiSrc := pick(r.Upper, layers, top)
			if lo.(int64) < hi.(int64) {
				if top != ScopeSession {
					warnings = append(warnings, fmt.Sprintf("%s must be below %s; using the %s values", r.Lower, r.Upper, max(loSrc, hiSrc)))
				}
				values[r.Lower], sources[r.Lower] = lo, loSrc
				values[r.Upper], sources[r.Upper] = hi, hiSrc
				break
			}
		}
	}
	return &Snapshot{Settings: project(values), values: values, sources: sources, Warnings: warnings}
}

// pick resolves key from the layers up to and including top.
func pick(key string, layers []map[string]any, top Scope) (any, Scope) {
	v, src := definitions[key].Default, ScopeDefault
	for scope := ScopeUser; scope <= top; scope++ {
		if x, ok := layers[scope][key]; ok {
			v, src = x, scope
		}
	}
	return v, src
}

func notify(subs []func(*Snapshot), snap *Snapshot) {
	for _, fn := range subs {
		fn(snap)
	}
}
