package antigravity

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

var recovery = map[string]string{
	"RATE_LIMIT_EXCEEDED":             "Wait for this model's quota to reset or select another model.",
	"RESOURCE_EXHAUSTED":              "Wait for this model's quota to reset or select another model.",
	"INSUFFICIENT_G1_CREDITS_BALANCE": "Check your AI credits balance in Antigravity before retrying.",
	"VALIDATION_REQUIRED":             "Open Antigravity and complete the account verification before retrying.",
	"MODEL_CAPACITY_EXHAUSTED":        "Retry later or select another model while capacity is unavailable.",
	"PROMPT_BLOCKED":                  "Rephrase the request; the backend refused the prompt.",
	"UNAUTHENTICATED":                 "Sign in again with /login antigravity.",
}

// RPCError is a redacted Cloud Code failure with a reason and a recovery action.
type RPCError struct {
	Reason     string
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *RPCError) Error() string {
	status := "unknown"
	if e.Status > 0 {
		status = strconv.Itoa(e.Status)
	}
	rec, ok := recovery[e.Reason]
	if !ok {
		rec = "Retry the request; if it persists, sign in with /login antigravity."
	}
	return fmt.Sprintf("antigravity: streamGenerateContent (%s, HTTP %s): %s; %s", e.Reason, status, e.Message, rec)
}

var retryDelay = regexp.MustCompile(`^(\d+(?:\.\d+)?)s$`)

// ParseRPCError reads a google.rpc Status, an error envelope or plain text; secrets never
// reach the message.
func ParseRPCError(body []byte, status int, secrets ...string) *RPCError {
	var raw any
	if json.Unmarshal(body, &raw) != nil {
		raw = map[string]any{"message": string(body)}
	}
	obj, _ := raw.(map[string]any)
	if inner, ok := obj["response"].(map[string]any); ok {
		obj = inner
	}
	if inner, ok := obj["error"].(map[string]any); ok {
		obj = inner
	}
	e := &RPCError{Reason: "UNKNOWN", Status: status, Message: "Request failed"}
	if s, ok := obj["status"].(string); ok && s != "" {
		e.Reason = s
	}
	if m, ok := obj["message"].(string); ok && m != "" {
		e.Message = m
	}
	if e.Status == 0 {
		if c, ok := obj["code"].(float64); ok {
			e.Status = int(c)
		}
	}
	details, _ := obj["details"].([]any)
	for _, d := range details {
		detail, _ := d.(map[string]any)
		if r, ok := detail["reason"].(string); ok && r != "" {
			e.Reason = r
		}
		if s, ok := detail["retryDelay"].(string); ok {
			if m := retryDelay.FindStringSubmatch(s); m != nil {
				if f, err := strconv.ParseFloat(m[1], 64); err == nil {
					e.RetryAfter = time.Duration(f * float64(time.Second))
				}
			}
		}
	}
	e.Reason = httpx.Redact(e.Reason, secrets...)
	e.Message = httpx.Redact(truncate(e.Message, 500), secrets...)
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var missingSignature = regexp.MustCompile(`(?is)\b(?:function\s*call|tool\s*call)\b.*(?:\bmissing\b.{0,200}\bthought[_\s]?signature\b|\bthought[_\s]?signature\b.{0,200}\bmissing\b)`)

// isMissingSignature reports Cloud Code's 400 for a function call without its thought signature.
func isMissingSignature(status int, body []byte) bool {
	return status == 400 && missingSignature.Match(body)
}
