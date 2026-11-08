package wire

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Client is the shared HTTP client for provider traffic. It has no overall timeout because
// responses stream; the request context cancels them.
var Client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   4,
}}

// Exchange shapes one provider request and response. Nil members leave that side unchanged.
type Exchange struct {
	// Signal is the provider request's cancellation; it aborts the HTTP exchange, which is the
	// only way to stop the inner wire stream.
	Signal context.Context
	// Request receives the host-built request with its body buffered in Body; it may replace
	// the URL, headers and body.
	Request func(r *Request) error
	// Response may replace the response, for example to wrap its body in a stream rewriter.
	Response func(r *http.Response) (*http.Response, error)
}

// Request is a host-built provider request with a buffered body.
type Request struct {
	HTTP *http.Request
	Body []byte
}

// Fetch returns the request-local transport for one stream.
func Fetch(client *http.Client, x Exchange) sdk.ModelFetchFunc {
	return func(hr *http.Request) (*http.Response, error) {
		var body []byte
		if hr.Body != nil {
			b, err := io.ReadAll(hr.Body)
			_ = hr.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("read provider request body: %w", err)
			}
			body = b
		}
		req := &Request{HTTP: hr, Body: body}
		if x.Request != nil {
			if err := x.Request(req); err != nil {
				return nil, err
			}
		}
		out := req.HTTP
		cancel := func() {}
		if x.Signal != nil {
			var ctx context.Context
			ctx, cancel = context.WithCancel(out.Context())
			stop := context.AfterFunc(x.Signal, cancel)
			release := cancel
			cancel = func() { stop(); release() }
			out = out.WithContext(ctx)
		}
		out.Body = io.NopCloser(bytes.NewReader(req.Body))
		out.ContentLength = int64(len(req.Body))
		out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(req.Body)), nil }
		// Go only decodes gzip transparently when it chose the encoding itself; the host would
		// otherwise receive compressed bytes it never asked to decode.
		out.Header.Del("Accept-Encoding")
		out.Header.Del("Content-Length")
		resp, err := client.Do(out)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
		if x.Response != nil {
			wrapped, err := x.Response(resp)
			if err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
			resp = wrapped
		}
		return resp, nil
	}
}

// cancelBody releases the exchange's cancellation when the host closes the body.
type cancelBody struct {
	io.ReadCloser
	cancel func()
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
