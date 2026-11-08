package quota

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/fsx"
)

// DefaultLease is how long a refresh lease blocks other sessions.
const DefaultLease = 30 * time.Second

// Entry is one cached value with its freshness.
type Entry[T any] struct {
	Value T `json:"value"`
	// FetchedAt is the last successful endpoint fetch (epoch ms).
	FetchedAt int64 `json:"fetchedAt"`
	// UpdatedAt orders concurrent writers: the newest entry wins on merge.
	UpdatedAt int64 `json:"updatedAt"`
	// ParserVersion invalidates entries written by older parsers.
	ParserVersion int `json:"parserVersion"`
}

// Backoff holds a key back until a time after a failed fetch.
type Backoff struct {
	Until  int64  `json:"until"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CacheOptions configure a [Cache].
type CacheOptions[T any] struct {
	Path string
	// Parse validates a stored value at the read boundary; false drops the entry.
	Parse func(raw json.RawMessage) (T, bool)
	// Version returns the current parser version for key.
	Version func(key string) int
	Now     func() time.Time
	Lease   time.Duration
	// HoldStatuses are HTTP statuses whose backoff a forced refresh must respect.
	HoldStatuses []int
}

type diskPayload struct {
	Version int                        `json:"version"`
	Entries map[string]json.RawMessage `json:"entries"`
	Leases  map[string]int64           `json:"leases"`
	Backoff map[string]Backoff         `json:"backoff"`
}

// Cache is a host-shared JSON cache with newest-wins entries, refresh leases and failure
// backoff. Every mutation is read-merge-write under a cross-process file lock.
type Cache[T any] struct {
	o       CacheOptions[T]
	mu      sync.Mutex
	entries map[string]Entry[T]
	raw     map[string]json.RawMessage
	leases  map[string]int64
	backoff map[string]Backoff
	mtime   time.Time
	size    int64
}

// NewCache creates an empty cache; call [Cache.Sync] to load the file.
func NewCache[T any](o CacheOptions[T]) *Cache[T] {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Lease == 0 {
		o.Lease = DefaultLease
	}
	return &Cache[T]{o: o, entries: map[string]Entry[T]{}, raw: map[string]json.RawMessage{}, leases: map[string]int64{}, backoff: map[string]Backoff{}}
}

func (c *Cache[T]) nowMs() int64 { return c.o.Now().UnixMilli() }

// Get returns the cached entry for key.
func (c *Cache[T]) Get(key string) (Entry[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok
}

// Backoff returns the active backoff for key.
func (c *Cache[T]) Backoff(key string) (Backoff, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeBackoff(key)
}

func (c *Cache[T]) activeBackoff(key string) (Backoff, bool) {
	b, ok := c.backoff[key]
	return b, ok && b.Until > c.nowMs()
}

// Sync merges what other sessions wrote and reports whether any entry changed.
func (c *Cache[T]) Sync(ctx context.Context, force bool) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncLocked(ctx, force)
}

func (c *Cache[T]) syncLocked(ctx context.Context, force bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := os.Stat(c.o.Path)
	if err != nil {
		return false, nil
	}
	if !force && info.ModTime().Equal(c.mtime) && info.Size() == c.size {
		return false, nil
	}
	data, err := fsx.ReadOptional(c.o.Path)
	if err != nil {
		return false, err
	}
	changed := c.merge(data)
	c.mtime, c.size = info.ModTime(), info.Size()
	return changed, nil
}

// merge folds a file's content in; corrupt or incompatible files contribute nothing.
func (c *Cache[T]) merge(data []byte) bool {
	var p diskPayload
	if len(data) == 0 || json.Unmarshal(data, &p) != nil || p.Version != 1 {
		return false
	}
	changed := false
	for key, raw := range p.Entries {
		var head struct {
			Value         json.RawMessage `json:"value"`
			FetchedAt     int64           `json:"fetchedAt"`
			UpdatedAt     *int64          `json:"updatedAt"`
			ParserVersion int             `json:"parserVersion"`
		}
		if json.Unmarshal(raw, &head) != nil || head.ParserVersion < c.o.Version(key) {
			continue
		}
		updated := head.FetchedAt
		if head.UpdatedAt != nil {
			updated = *head.UpdatedAt
		}
		value, ok := c.o.Parse(head.Value)
		if !ok {
			continue
		}
		existing, have := c.entries[key]
		if have && (existing.UpdatedAt > updated || existing.UpdatedAt == updated && string(c.raw[key]) == string(raw)) {
			continue
		}
		c.entries[key] = Entry[T]{Value: value, FetchedAt: head.FetchedAt, UpdatedAt: updated, ParserVersion: head.ParserVersion}
		c.raw[key] = raw
		changed = true
	}
	for key, until := range p.Leases {
		c.leases[key] = max(c.leases[key], until)
	}
	for key, b := range p.Backoff {
		if b.Until >= c.backoff[key].Until {
			c.backoff[key] = b
		}
	}
	return changed
}

// mutate runs change between a forced sync and an atomic write, under the file lock. change
// returns false to skip the write.
func (c *Cache[T]) mutate(ctx context.Context, change func() bool) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	unlock, err := fsx.Lock(ctx, c.o.Path)
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()
	if _, err := c.syncLocked(ctx, true); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !change() {
		return false, nil
	}
	if err := c.write(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Cache[T]) write() error {
	now := c.nowMs()
	p := diskPayload{Version: 1, Entries: map[string]json.RawMessage{}, Leases: map[string]int64{}, Backoff: map[string]Backoff{}}
	for key, e := range c.entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		c.raw[key] = raw
		p.Entries[key] = raw
	}
	for key, until := range c.leases {
		if until > now {
			p.Leases[key] = until
		}
	}
	for key, b := range c.backoff {
		if b.Until > now {
			p.Backoff[key] = b
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := fsx.WriteAtomic(c.o.Path, data); err != nil {
		return err
	}
	if info, err := os.Stat(c.o.Path); err == nil {
		c.mtime, c.size = info.ModTime(), info.Size()
	}
	return nil
}

// AcquireOptions say when a refresh may start.
type AcquireOptions struct {
	// MinAge skips the refresh when the entry is younger than this.
	MinAge time.Duration
	// Force ignores freshness, leases and ordinary backoff (a user-requested refresh).
	Force bool
}

// TryAcquire reports whether this session should fetch key; it then holds a lease other
// sessions respect. A forced refresh never bypasses a hold status backoff (403/429).
func (c *Cache[T]) TryAcquire(ctx context.Context, key string, o AcquireOptions) (bool, error) {
	acquired := false
	_, err := c.mutate(ctx, func() bool {
		now := c.nowMs()
		if b, ok := c.activeBackoff(key); ok && (!o.Force || slices.Contains(c.o.HoldStatuses, b.Status)) {
			return false
		}
		if !o.Force {
			if c.leases[key] > now {
				return false
			}
			if e, ok := c.entries[key]; ok && now-e.FetchedAt < o.MinAge.Milliseconds() {
				return false
			}
		}
		c.leases[key] = now + c.o.Lease.Milliseconds()
		acquired = true
		return true
	})
	return acquired && err == nil, err
}

// Put stores a fresh value, releases the lease and clears backoff.
func (c *Cache[T]) Put(ctx context.Context, key string, value T) error {
	return c.PutWith(ctx, key, func(T, bool) T { return value })
}

// PutWith derives the value from the entry current under the file lock.
func (c *Cache[T]) PutWith(ctx context.Context, key string, change func(current T, ok bool) T) error {
	_, err := c.mutate(ctx, func() bool {
		now := c.nowMs()
		cur, ok := c.entries[key]
		c.entries[key] = Entry[T]{Value: change(cur.Value, ok), FetchedAt: now, UpdatedAt: now, ParserVersion: c.o.Version(key)}
		delete(c.leases, key)
		delete(c.backoff, key)
		return true
	})
	return err
}

// Patch changes a value without refreshing endpoint freshness, releasing a lease or clearing
// backoff; change returns false to leave the entry alone.
func (c *Cache[T]) Patch(ctx context.Context, key string, change func(current T, ok bool) (T, bool)) (bool, error) {
	return c.mutate(ctx, func() bool {
		cur, ok := c.entries[key]
		value, keep := change(cur.Value, ok)
		if !keep {
			return false
		}
		version := cur.ParserVersion
		if !ok {
			version = c.o.Version(key)
		}
		c.entries[key] = Entry[T]{Value: value, FetchedAt: cur.FetchedAt, UpdatedAt: c.nowMs(), ParserVersion: version}
		return true
	})
}

// Fail records a failed fetch: the stored value stays and the key backs off.
func (c *Cache[T]) Fail(ctx context.Context, key string, backoff time.Duration, status int, message string) error {
	_, err := c.mutate(ctx, func() bool {
		delete(c.leases, key)
		c.backoff[key] = Backoff{Until: c.nowMs() + backoff.Milliseconds(), Status: status, Error: message}
		return true
	})
	return err
}

// Keys returns the cached keys, sorted.
func (c *Cache[T]) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.entries))
}

// BackoffPolicy chooses how long a failure holds a provider back.
type BackoffPolicy struct {
	Default, RateLimit, Max time.Duration
	// RateLimitStatuses honor Retry-After; other statuses use Default.
	RateLimitStatuses []int
}

// For returns the backoff for a failure; retryAfter is zero when the server gave none.
func (p BackoffPolicy) For(status int, retryAfter time.Duration) time.Duration {
	if status == 0 || !slices.Contains(p.RateLimitStatuses, status) {
		return p.Default
	}
	d := p.RateLimit
	if retryAfter > 0 {
		d = retryAfter
	}
	return min(d, p.Max)
}
