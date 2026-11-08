package tokens

import (
	"context"
	"time"
)

// Counter counts content for one model. Every method must be safe for concurrent use.
type Counter interface {
	// Text counts plain text with the best source available now; it never fails.
	Text(ctx context.Context, text string, k Kind) Count
	// AnchorSource says where the persisted assistant usage of this model comes from.
	AnchorSource() Source
}

// ToolCounter is implemented by counters that encode tool definitions themselves.
type ToolCounter interface {
	Tools(ctx context.Context, tools []ToolSchema) (Count, bool)
}

// NativeCounter is implemented by counters that count a complete request. It reports false when
// the request cannot be counted natively, so the engine keeps its labelled estimate.
type NativeCounter interface {
	Context(ctx context.Context, in NativeInput) (Count, bool)
}

// SystemRewriter is implemented by counters whose provider rewrites the system prompt before sending.
type SystemRewriter interface {
	SystemText(text string) string
}

// NativeInput is a complete supported request for native counting.
type NativeInput struct {
	Provider, Model string
	Messages        []map[string]any
	// SystemPrompt and Tools are set only when the transcript records no system messages.
	SystemPrompt  string
	Tools         []ToolSchema
	ThinkingLevel string
}

// Input is the live session state a report is computed from.
type Input struct {
	Provider, Model string
	ContextWindow   int
	ThinkingLevel   string
	Messages        []map[string]any
	Entries         []ProjectedEntry
	Branch          []BranchEntry
	LiveSystem      string
	LiveTools       []ToolSchema
	// HostTokens is the host's own count; nil when it reports none.
	HostTokens *int
}

// Report is a context count with its anchor, tail and the host's comparison number.
type Report struct {
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	ContextWindow int       `json:"contextWindow,omitempty"`
	Tokens        Count     `json:"tokens"`
	Percent       *float64  `json:"percent,omitempty"`
	Anchor        *Count    `json:"anchor,omitempty"`
	Tail          Count     `json:"tail"`
	TailMessages  int       `json:"tailMessages"`
	HostTokens    *int      `json:"host,omitempty"`
	ComputedAt    time.Time `json:"computedAt"`
}

// Kind of a breakdown part.
const (
	PartSystem     = "system"
	PartTools      = "tools"
	PartUser       = "user"
	PartAssistant  = "assistant"
	PartToolResult = "toolResult"
	PartOther      = "other"
)

// Part is the count of one logical contribution to the full request.
type Part struct {
	Kind  string `json:"kind"`
	Count Count  `json:"count"`
	Items int    `json:"items"`
}

// Engine selects counters and computes reports. It holds no session state.
type Engine struct {
	// For returns the counter for a provider and model.
	For func(provider, model string) Counter
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func countSegments(ctx context.Context, c Counter, segs []Segment) (Count, bool) {
	counts := make([]Count, 0, len(segs))
	for _, s := range segs {
		if ctx.Err() != nil {
			return Count{}, false
		}
		if s.Image {
			counts = append(counts, ImageCount())
			continue
		}
		counts = append(counts, c.Text(ctx, s.Text, s.Kind))
	}
	return Sum(counts...), true
}

func countTools(ctx context.Context, c Counter, tools []ToolSchema) (Count, bool) {
	if ctx.Err() != nil {
		return Count{}, false
	}
	if len(tools) == 0 {
		return Sum(), true
	}
	if tc, ok := c.(ToolCounter); ok {
		if n, ok := tc.Tools(ctx, tools); ok {
			return n, ctx.Err() == nil
		}
	}
	segs := make([]Segment, len(tools))
	for i, t := range tools {
		segs[i] = Segment{Text: jsonText(t), Kind: JSON}
	}
	return countSegments(ctx, c, segs)
}

func asSent(c Counter, segs []Segment) []Segment {
	rw, ok := c.(SystemRewriter)
	if !ok {
		return segs
	}
	out := make([]Segment, len(segs))
	for i, s := range segs {
		if !s.Image && s.Kind == Prose {
			s.Text = rw.SystemText(s.Text)
		}
		out[i] = s
	}
	return out
}

type systemInputs struct {
	segs  []Segment
	tools []ToolSchema
	items int
}

// systemFor uses the transcript's recorded system messages, and the live prompt and tools only
// before the first one is recorded.
func systemFor(c Counter, in Input) systemInputs {
	if st, ok := CurrentSystem(in.Messages); ok {
		return systemInputs{asSent(c, st.Segments), st.Tools, st.Messages}
	}
	return systemInputs{asSent(c, []Segment{{Text: in.LiveSystem, Kind: Prose}}), in.LiveTools, 1}
}

// Context counts the whole context: the last valid usage plus every message after it, or the
// full request when no usage anchors it. It reports false when ctx is cancelled.
func (e *Engine) Context(ctx context.Context, in Input) (Report, bool) {
	counter := e.For(in.Provider, in.Model)
	anchor, anchored := FindAnchor(in.Messages, in.Entries, in.Branch)
	tail := in.Messages
	if anchored {
		tail = in.Messages[anchor.Index+1:]
	}
	var counts []Count
	var native bool
	if !anchored {
		if nc, ok := counter.(NativeCounter); ok {
			if ni, ok := nativeInput(in); ok {
				if n, ok := nc.Context(ctx, ni); ok {
					counts = append(counts, n)
					native = true
				}
			}
		}
	}
	if ctx.Err() != nil {
		return Report{}, false
	}
	if !native {
		if !anchored {
			sys := systemFor(counter, in)
			n, ok := countSegments(ctx, counter, sys.segs)
			if !ok {
				return Report{}, false
			}
			counts = append(counts, n)
			if n, ok = countTools(ctx, counter, sys.tools); !ok {
				return Report{}, false
			}
			counts = append(counts, n)
		}
		for _, m := range tail {
			role := str(m["role"])
			if !anchored && role == "system" {
				continue
			}
			segs := MessageSegments(m)
			if role == "system" {
				segs = asSent(counter, segs)
			}
			n, ok := countSegments(ctx, counter, segs)
			if !ok {
				return Report{}, false
			}
			counts = append(counts, n)
		}
	}
	tailCount := Sum(counts...)
	total := tailCount
	var anchorCount *Count
	if anchored {
		a := Count{Tokens: anchor.Tokens, Source: counter.AnchorSource()}
		anchorCount = &a
		total = Sum(a, tailCount)
	}
	r := Report{
		Provider: in.Provider, Model: in.Model, ContextWindow: in.ContextWindow,
		Tokens: total, Anchor: anchorCount, Tail: tailCount, TailMessages: len(tail),
		HostTokens: in.HostTokens, ComputedAt: e.now(),
	}
	if in.ContextWindow > 0 {
		p := float64(total.Tokens) / float64(in.ContextWindow) * 100
		r.Percent = &p
	}
	return r, true
}

func partKind(role string) string {
	switch role {
	case "system", "user", "assistant", "toolResult":
		return role
	}
	return PartOther
}

// Breakdown recounts the full request by contribution, independent of usage anchors.
func (e *Engine) Breakdown(ctx context.Context, in Input) ([]Part, bool) {
	counter := e.For(in.Provider, in.Model)
	sys := systemFor(counter, in)
	system, ok := countSegments(ctx, counter, sys.segs)
	if !ok {
		return nil, false
	}
	tools, ok := countTools(ctx, counter, sys.tools)
	if !ok {
		return nil, false
	}
	parts := []Part{{PartSystem, system, sys.items}, {PartTools, tools, len(sys.tools)}}
	index := map[string]int{PartSystem: 0, PartTools: 1}
	for _, m := range in.Messages {
		role := str(m["role"])
		if role == "system" || (role == "bashExecution" && m["excludeFromContext"] == true) {
			continue
		}
		n, ok := countSegments(ctx, counter, MessageSegments(m))
		if !ok {
			return nil, false
		}
		kind := partKind(role)
		i, seen := index[kind]
		if !seen {
			i = len(parts)
			index[kind] = i
			parts = append(parts, Part{Kind: kind, Count: Sum()})
		}
		parts[i].Count = Sum(parts[i].Count, n)
		parts[i].Items++
	}
	if ctx.Err() != nil {
		return nil, false
	}
	return parts, true
}

// nativeInput reports whether the transcript can be counted as one native request and shapes it.
func nativeInput(in Input) (NativeInput, bool) {
	out := NativeInput{Provider: in.Provider, Model: in.Model, ThinkingLevel: in.ThinkingLevel}
	recorded := false
	for _, m := range in.Messages {
		switch str(m["role"]) {
		case "bashExecution":
			if m["excludeFromContext"] == true {
				continue
			}
			return NativeInput{}, false
		case "system":
			recorded = true
		case "user", "assistant", "toolResult":
		default:
			return NativeInput{}, false
		}
		if !supportedContent(m) {
			return NativeInput{}, false
		}
		out.Messages = append(out.Messages, m)
	}
	if !recorded {
		out.SystemPrompt, out.Tools = in.LiveSystem, in.LiveTools
	}
	return out, true
}

func supportedContent(m map[string]any) bool {
	if str(m["role"]) == "assistant" {
		switch str(m["stopReason"]) {
		case "pending", "deferred", "error", "aborted":
			return false
		}
		for _, raw := range anySlice(m["content"]) {
			b := asMap(raw)
			switch str(b["type"]) {
			case "text":
				if b["textSignature"] != nil {
					return false
				}
			case "thinking":
				if b["thinkingSignature"] != nil || b["redacted"] == true {
					return false
				}
			case "toolCall":
				if b["thoughtSignature"] != nil || b["argumentsError"] != nil {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if _, isString := m["content"].(string); isString {
		return true
	}
	for _, raw := range anySlice(m["content"]) {
		b := asMap(raw)
		if str(b["type"]) != "text" || b["textSignature"] != nil {
			return false
		}
	}
	return true
}

// EstimateCounter counts with measured characters-per-token ratios only.
type EstimateCounter struct{ Ratios Ratios }

// Text implements Counter.
func (c EstimateCounter) Text(_ context.Context, text string, k Kind) Count {
	return c.Ratios.Estimate(text, k)
}

// AnchorSource implements Counter: stored usage comes from the provider.
func (EstimateCounter) AnchorSource() Source { return SourceProvider }
