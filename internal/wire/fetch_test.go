package wire

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchShapesRequestAndDecodesGzip(t *testing.T) {
	var gotBody, gotHeader, gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotHeader, gotEncoding = string(b), r.Header.Get("X-Shape"), r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/event-stream")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte("data: ok\n\n"))
		_ = zw.Close()
	}))
	defer srv.Close()

	fetch := Fetch(srv.Client(), Exchange{
		Request: func(r *Request) error {
			r.HTTP.Header.Set("X-Shape", "yes")
			r.Body = append(r.Body, []byte(`-shaped`)...)
			return nil
		},
		Response: func(r *http.Response) (*http.Response, error) {
			r.Body = io.NopCloser(io.MultiReader(r.Body, strings.NewReader("data: tail\n\n")))
			return r, nil
		},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(`{"a":1}`)))
	// The host's fetch forwards its own Accept-Encoding; Fetch must drop it so Go decodes gzip.
	req.Header.Set("Accept-Encoding", "gzip, br")
	resp, err := fetch(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if gotBody != `{"a":1}-shaped` || gotHeader != "yes" {
		t.Fatalf("request not shaped: body=%q header=%q", gotBody, gotHeader)
	}
	if strings.Contains(gotEncoding, "br") {
		t.Fatalf("host Accept-Encoding forwarded: %q", gotEncoding)
	}
	if string(got) != "data: ok\n\ndata: tail\n\n" {
		t.Fatalf("response = %q", got)
	}
}

func TestFetchSignalAbortsStreamingBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	signal, abort := context.WithCancel(context.Background())
	fetch := Fetch(srv.Client(), Exchange{Signal: signal})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	resp, err := fetch(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	abort()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("body ended cleanly after the signal was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signal did not abort the streaming body")
	}
}
