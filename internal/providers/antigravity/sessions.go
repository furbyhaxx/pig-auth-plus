package antigravity

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionTTL  = 24 * time.Hour
	maxSessions = 256
	// workspaceSessionID is the CLI's FNV-1a hash of an empty workspace URI.
	workspaceSessionID = "-3750763034362895579"
)

// Sessions keeps the CLI's per-conversation trajectory metadata.
type Sessions struct {
	Now  func() time.Time
	UUID func() string

	mu      sync.Mutex
	entries map[string]*trajectory
}

type trajectory struct {
	conversation, id string
	accessed         time.Time
	timestamp        int64
	usedClaude       bool
	usedNonGemini    bool
}

// Metadata is one request's identity in the CLI's form.
type Metadata struct {
	RequestID string
	Labels    map[string]any
	SessionID string
}

// Begin records one logical request of conversation key and returns its metadata. step is the
// index of the last content, as the CLI counts it.
func (s *Sessions) Begin(key, model, enum string, step int) Metadata {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	uuid := newUUID
	if s.UUID != nil {
		uuid = s.UUID
	}
	t := now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]*trajectory{}
	}
	for k, e := range s.entries {
		if k != key && t.Sub(e.accessed) > sessionTTL {
			delete(s.entries, k)
		}
	}
	e := s.entries[key]
	if e == nil {
		if len(s.entries) >= maxSessions {
			oldest := ""
			for k, v := range s.entries {
				if oldest == "" || v.accessed.Before(s.entries[oldest].accessed) {
					oldest = k
				}
			}
			delete(s.entries, oldest)
		}
		e = &trajectory{conversation: uuid(), id: uuid()}
		s.entries[key] = e
	}
	e.accessed = t
	e.timestamp = max(t.UnixMilli(), e.timestamp+1)
	claude := strings.HasPrefix(model, "claude-")
	e.usedClaude = e.usedClaude || claude
	e.usedNonGemini = e.usedNonGemini || claude || strings.HasPrefix(model, "gpt-")
	requestID := fmt.Sprintf("agent/%s/%d/%s/%d", e.conversation, e.timestamp, e.id, step+1)
	return Metadata{
		RequestID: requestID,
		SessionID: workspaceSessionID,
		Labels: map[string]any{
			"cascade_id":               e.conversation,
			"last_step_index":          strconv.Itoa(step),
			"model_enum":               enum,
			"request_id":               requestID,
			"root_cascade_id":          e.conversation,
			"trajectory_id":            e.id,
			"used_claude":              strconv.FormatBool(e.usedClaude),
			"used_claude_conservative": strconv.FormatBool(e.usedClaude),
			"used_non_gemini_model":    strconv.FormatBool(e.usedNonGemini),
		},
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
