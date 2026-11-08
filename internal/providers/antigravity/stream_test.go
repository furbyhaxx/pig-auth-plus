package antigravity

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUnwrapSSEPassesChunksAndFailsOnInBandErrors(t *testing.T) {
	src := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]},\"traceId\":\"t\"}\n\n" +
		"data: {\"response\":{\"error\":{\"code\":429,\"message\":\"quota for tok-1234567890abcdef\",\"details\":[{\"reason\":\"RATE_LIMIT_EXCEEDED\"}]}}}\n\n"
	r := newUnwrapSSE(io.NopCloser(strings.NewReader(src)), "tok-1234567890abcdef")
	got, err := io.ReadAll(r)
	if !strings.Contains(string(got), "data: {\"candidates\"") || strings.Contains(string(got), "traceId") {
		t.Fatalf("output = %q", got)
	}
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Reason != "RATE_LIMIT_EXCEEDED" || strings.Contains(err.Error(), "tok-1234567890abcdef") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRPCErrorReadsRetryDelay(t *testing.T) {
	e := ParseRPCError([]byte(`[{"error":{"code":429}}]`), 429)
	if e.Reason != "UNKNOWN" || e.Status != 429 {
		t.Fatalf("e = %+v", e)
	}
	e = ParseRPCError([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"m","details":[{"retryDelay":"1.5s"}]}}`), 0)
	if e.Status != 429 || e.Reason != "RESOURCE_EXHAUSTED" || e.RetryAfter.Milliseconds() != 1500 {
		t.Fatalf("e = %+v", e)
	}
}
