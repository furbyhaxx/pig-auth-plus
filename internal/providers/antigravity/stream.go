package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// unwrapSSE turns Cloud Code's `data: {"response": chunk}` frames into plain Gemini chunks.
// An in-band error or a blocked prompt ends the body with an RPCError, which PiG reports as
// the turn's error.
type unwrapSSE struct {
	src     io.ReadCloser
	lines   *bufio.Reader
	pending bytes.Buffer
	err     error
	secrets []string
}

func newUnwrapSSE(body io.ReadCloser, secrets ...string) *unwrapSSE {
	return &unwrapSSE{src: body, lines: bufio.NewReaderSize(body, 64<<10), secrets: secrets}
}

func (u *unwrapSSE) Read(p []byte) (int, error) {
	for u.pending.Len() == 0 {
		if u.err != nil {
			return 0, u.err
		}
		line, err := u.lines.ReadBytes('\n')
		if len(line) > 0 {
			if rerr := u.line(line); rerr != nil {
				u.err = rerr
				continue
			}
		}
		if err != nil {
			u.err = err
		}
	}
	return u.pending.Read(p)
}

func (u *unwrapSSE) line(line []byte) error {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		u.pending.Write(line)
		return nil
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}
	var frame map[string]json.RawMessage
	if json.Unmarshal(data, &frame) != nil {
		return &RPCError{Reason: "INVALID_SSE", Message: "Invalid JSON in stream frame"}
	}
	chunk := data
	if inner, ok := frame["response"]; ok && len(inner) > 0 && inner[0] == '{' {
		chunk = inner
		frame = nil
		_ = json.Unmarshal(inner, &frame)
	}
	if raw, ok := frame["error"]; ok {
		return ParseRPCError(raw, 0, u.secrets...)
	}
	if raw, ok := frame["promptFeedback"]; ok {
		var fb struct {
			BlockReason        string `json:"blockReason"`
			BlockReasonMessage string `json:"blockReasonMessage"`
		}
		if json.Unmarshal(raw, &fb) == nil && (fb.BlockReason != "" || fb.BlockReasonMessage != "") {
			msg := fb.BlockReason + ": " + fb.BlockReasonMessage
			return &RPCError{Reason: "PROMPT_BLOCKED", Message: msg}
		}
	}
	u.pending.WriteString("data: ")
	u.pending.Write(chunk)
	u.pending.WriteString("\n")
	return nil
}

func (u *unwrapSSE) Close() error { return u.src.Close() }
