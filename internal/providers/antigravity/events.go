package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// usageGrace is how long gpt-oss may send its usage chunk after the finish reason.
const usageGrace = time.Second

// output is the assistant message under construction.
type output struct {
	model   map[string]any
	content []map[string]any
	usage   map[string]any
	stop    string
	raw     string
	id      string
	started int64
	rates   map[string]any
}

func newOutput(model map[string]any, now time.Time) *output {
	rates, _ := model["cost"].(map[string]any)
	return &output{model: model, started: now.UnixMilli(), rates: rates, usage: usageMap(0, 0, 0, 0, 0, rates)}
}

// message is a snapshot: later deltas never mutate an event already pushed.
func (o *output) message() map[string]any {
	content := make([]any, len(o.content))
	for i, b := range o.content {
		content[i] = maps.Clone(b)
	}
	m := map[string]any{
		"role": "assistant", "api": o.model["api"], "provider": o.model["provider"], "model": o.model["id"],
		"content": content, "stopReason": o.stop, "timestamp": o.started, "usage": maps.Clone(o.usage),
	}
	if o.stop == "" {
		m["stopReason"] = "stop"
	}
	if o.id != "" {
		m["responseId"] = o.id
	}
	return m
}

func usageMap(input, output, reasoning, cacheRead, total int, rates map[string]any) map[string]any {
	rate := func(k string) float64 { f, _ := rates[k].(float64); return f }
	cost := map[string]any{
		"input": float64(input) * rate("input") / 1e6, "output": float64(output) * rate("output") / 1e6,
		"cacheRead": float64(cacheRead) * rate("cacheRead") / 1e6, "cacheWrite": 0.0,
	}
	cost["total"] = cost["input"].(float64) + cost["output"].(float64) + cost["cacheRead"].(float64)
	u := map[string]any{"input": input, "output": output, "cacheRead": cacheRead, "cacheWrite": 0, "totalTokens": total, "cost": cost}
	if reasoning > 0 {
		u["reasoning"] = reasoning
	}
	return u
}

// chunk is the part of a Gemini stream chunk this provider reads.
type chunk struct {
	ResponseID string `json:"responseId"`
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text             *string       `json:"text"`
				Thought          bool          `json:"thought"`
				ThoughtSignature string        `json:"thoughtSignature"`
				FunctionCall     *functionCall `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Usage *struct {
		Prompt     int `json:"promptTokenCount"`
		Candidates int `json:"candidatesTokenCount"`
		Thoughts   int `json:"thoughtsTokenCount"`
		Cached     int `json:"cachedContentTokenCount"`
		Total      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

type functionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// streamer turns Cloud Code SSE into PiG assistant events.
type streamer struct {
	out     *sdk.ModelEventStream
	o       *output
	current map[string]any
	pending string // a signature that arrived before its block
	ids     bool
	nextID  int
	now     func() time.Time
}

func (s *streamer) push(event map[string]any) {
	event["partial"] = s.o.message()
	s.out.Push(event)
}

func (s *streamer) index() int { return len(s.o.content) - 1 }

func (s *streamer) close() {
	if s.current == nil {
		return
	}
	if s.current["type"] == "text" {
		s.push(map[string]any{"type": "text_end", "contentIndex": s.index(), "content": s.current["text"]})
	} else {
		s.push(map[string]any{"type": "thinking_end", "contentIndex": s.index(), "content": s.current["thinking"]})
	}
	s.current = nil
}

func sign(block map[string]any, sig string) {
	if sig == "" {
		return
	}
	if block["type"] == "thinking" {
		block["thinkingSignature"] = sig
	} else {
		block["textSignature"] = sig
	}
}

func (s *streamer) handle(c *chunk) (terminal bool) {
	if c.ResponseID != "" && s.o.id == "" {
		s.o.id = c.ResponseID
	}
	if u := c.Usage; u != nil {
		input := max(0, u.Prompt-u.Cached)
		s.o.usage = usageMap(input, u.Candidates+u.Thoughts, u.Thoughts, u.Cached, u.Total, s.o.rates)
	}
	if len(c.Candidates) == 0 {
		return false
	}
	cand := c.Candidates[0]
	for _, part := range cand.Content.Parts {
		text := ""
		if part.Text != nil {
			text = *part.Text
		}
		if text == "" && part.ThoughtSignature != "" && part.FunctionCall == nil {
			if s.current != nil && (s.current["type"] == "thinking") == part.Thought {
				sign(s.current, part.ThoughtSignature)
			}
			if part.Thought || s.current == nil || s.current["type"] != "text" {
				s.pending = part.ThoughtSignature
			}
			continue
		}
		if text != "" {
			kind := "text"
			if part.Thought {
				kind = "thinking"
			}
			if s.current == nil || s.current["type"] != kind {
				s.close()
				s.current = map[string]any{"type": kind, kind: ""}
				s.o.content = append(s.o.content, s.current)
				s.push(map[string]any{"type": kind + "_start", "contentIndex": s.index()})
			}
			s.current[kind] = s.current[kind].(string) + text
			sig := part.ThoughtSignature
			if kind == "text" && sig == "" {
				sig = s.pending
				s.pending = ""
			}
			sign(s.current, sig)
			s.push(map[string]any{"type": kind + "_delta", "contentIndex": s.index(), "delta": text})
		}
		if call := part.FunctionCall; call != nil {
			s.close()
			id := call.ID
			if s.ids {
				id = normalizeToolCallID(id)
			}
			for id == "" || s.hasCall(id) {
				s.nextID++
				id = fmt.Sprintf("call_%d_%d", s.now().UnixMilli(), s.nextID)
			}
			args := call.Args
			if args == nil {
				args = map[string]any{}
			}
			block := map[string]any{"type": "toolCall", "id": id, "name": call.Name, "arguments": args}
			sig := part.ThoughtSignature
			if sig == "" {
				sig = s.pending
			}
			if sig != "" {
				block["thoughtSignature"] = sig
			}
			s.pending = ""
			s.o.content = append(s.o.content, block)
			delta, _ := json.Marshal(args)
			s.push(map[string]any{"type": "toolcall_start", "contentIndex": s.index()})
			s.push(map[string]any{"type": "toolcall_delta", "contentIndex": s.index(), "delta": string(delta)})
			s.push(map[string]any{"type": "toolcall_end", "contentIndex": s.index(), "toolCall": maps.Clone(block)})
		}
	}
	if cand.FinishReason == "" {
		return false
	}
	s.close()
	s.o.raw = cand.FinishReason
	switch cand.FinishReason {
	case "STOP":
		s.o.stop = "stop"
		if s.hasAnyCall() {
			s.o.stop = "toolUse"
		}
	case "MAX_TOKENS":
		s.o.stop = "length"
	default:
		s.o.stop = "error"
	}
	return true
}

func (s *streamer) hasCall(id string) bool {
	for _, b := range s.o.content {
		if b["type"] == "toolCall" && b["id"] == id {
			return true
		}
	}
	return false
}

func (s *streamer) hasAnyCall() bool {
	for _, b := range s.o.content {
		if b["type"] == "toolCall" {
			return true
		}
	}
	return false
}

// run reads body to the end and finishes the stream with done or error. idle bounds the wait
// for the next line; a closed signal aborts.
func (s *streamer) run(signal context.Context, body io.ReadCloser, idle time.Duration, waitForUsage bool, secrets []string) {
	defer body.Close()
	s.push(map[string]any{"type": "start"})
	err := s.read(signal, body, idle, waitForUsage)
	s.close()
	switch {
	case signal.Err() != nil:
		s.fail("aborted", &RPCError{Reason: "ABORTED", Message: "Request was aborted"})
	case err != nil:
		s.fail("error", err)
	case s.o.stop == "":
		s.fail("error", &RPCError{Reason: "TRUNCATED_STREAM", Message: "Stream ended without a terminal candidate"})
	case len(s.o.content) == 0:
		s.fail("error", &RPCError{Reason: "EMPTY_RESPONSE", Message: "Received an empty response"})
	case s.o.stop == "error":
		s.fail("error", &RPCError{Reason: "FINISH_REASON", Message: "Provider stopped with " + s.o.raw})
	default:
		msg := s.o.message()
		s.out.Push(map[string]any{"type": "done", "reason": s.o.stop, "message": msg})
	}
}

func (s *streamer) fail(reason string, err error) {
	var rpc *RPCError
	if !errors.As(err, &rpc) {
		rpc = &RPCError{Reason: "STREAM_FAILED", Message: err.Error()}
	}
	msg := s.o.message()
	msg["stopReason"] = reason
	msg["errorMessage"] = rpc.Error()
	s.out.Push(map[string]any{"type": "error", "reason": reason, "error": msg})
}

var errStalled = errors.New("stalled")

func (s *streamer) read(signal context.Context, body io.ReadCloser, idle time.Duration, waitForUsage bool) error {
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		r := bufio.NewReaderSize(body, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case lines <- line:
				case <-stop:
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				readErr <- err
				return
			}
		}
	}()
	terminal := false
	deadline := idle
	for {
		timer := time.NewTimer(deadline)
		select {
		case <-signal.Done():
			timer.Stop()
			_ = body.Close()
			return signal.Err()
		case <-timer.C:
			_ = body.Close()
			if terminal {
				return nil
			}
			return &RPCError{Reason: "STREAM_STALLED", Message: fmt.Sprintf("No data for %s", idle)}
		case err := <-readErr:
			timer.Stop()
			return err
		case line := <-lines:
			timer.Stop()
			data, ok := bytes.CutPrefix(line, []byte("data:"))
			if !ok {
				continue
			}
			data = bytes.TrimSpace(data)
			if len(data) == 0 {
				continue
			}
			var c chunk
			if err := json.Unmarshal(data, &c); err != nil {
				return &RPCError{Reason: "INVALID_SSE", Message: "Invalid JSON in stream chunk"}
			}
			if terminal {
				if c.Usage != nil {
					s.handle(&chunk{Usage: c.Usage})
					_ = body.Close()
					return nil
				}
				continue
			}
			if s.handle(&c) {
				terminal = true
				if !waitForUsage || c.Usage != nil {
					_ = body.Close()
					return nil
				}
				deadline = usageGrace
			}
		}
	}
}
