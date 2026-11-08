package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// maxLine bounds one SSE line or a whole JSON response held for rewriting.
const maxLine = 5 << 20

var nameField = regexp.MustCompile(`("name"[ \t]*:[ \t]*")((?:[^"\\\r\n]|\\.)*)(")`)

// restoreResponse rewrites wire tool names back to request names in an SSE or JSON body.
func restoreResponse(resp *http.Response, r restorer) *http.Response {
	if len(r) == 0 || resp.Body == nil {
		return resp
	}
	for _, h := range []string{"Content-Length", "Content-Encoding", "Content-Md5", "Content-Digest", "Digest", "Content-Range", "Etag"} {
		resp.Header.Del(h)
	}
	resp.ContentLength = -1
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &sseRestorer{src: resp.Body, in: bufio.NewReaderSize(resp.Body, 64<<10), restore: r}
		return resp
	}
	resp.Body = &jsonRestorer{src: resp.Body, restore: r}
	return resp
}

func rewriteNames(line []byte, r restorer) []byte {
	if !bytes.Contains(line, []byte(`name"`)) {
		return line
	}
	return nameField.ReplaceAllFunc(line, func(m []byte) []byte {
		sub := nameField.FindSubmatch(m)
		raw := sub[2]
		var wire string
		if err := json.Unmarshal(append(append([]byte{'"'}, raw...), '"'), &wire); err != nil {
			return m
		}
		name := r.restore(wire)
		if name == wire {
			return m
		}
		enc, _ := json.Marshal(name)
		out := append([]byte{}, sub[1]...)
		out = append(out, enc[1:len(enc)-1]...)
		return append(out, sub[3]...)
	})
}

// LimitError reports an upstream line or body over the rewrite bound.
type LimitError struct{ Kind string }

func (e *LimitError) Error() string {
	return fmt.Sprintf("claude: %s exceeds %s byte limit; retry with shorter content", e.Kind, strconv.Itoa(maxLine))
}

type sseRestorer struct {
	src     io.Closer
	in      *bufio.Reader
	restore restorer
	out     []byte
	err     error
}

func (s *sseRestorer) Read(p []byte) (int, error) {
	for len(s.out) == 0 && s.err == nil {
		line, err := s.readLine()
		if len(line) > 0 {
			s.out = rewriteNames(line, s.restore)
		}
		s.err = err
	}
	if len(s.out) > 0 {
		n := copy(p, s.out)
		s.out = s.out[n:]
		return n, nil
	}
	return 0, s.err
}

// readLine returns one line including its terminator, bounded by maxLine.
func (s *sseRestorer) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := s.in.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, &LimitError{Kind: "stream line"}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func (s *sseRestorer) Close() error { return s.src.Close() }

type jsonRestorer struct {
	src     io.ReadCloser
	restore restorer
	out     *bytes.Reader
	err     error
}

func (j *jsonRestorer) Read(p []byte) (int, error) {
	if j.out == nil && j.err == nil {
		data, err := io.ReadAll(io.LimitReader(j.src, maxLine+1))
		switch {
		case err != nil:
			j.err = err
		case len(data) > maxLine:
			j.err = &LimitError{Kind: "response"}
		default:
			j.out = bytes.NewReader(rewriteNames(data, j.restore))
		}
	}
	if j.err != nil {
		return 0, j.err
	}
	return j.out.Read(p)
}

func (j *jsonRestorer) Close() error { return j.src.Close() }
