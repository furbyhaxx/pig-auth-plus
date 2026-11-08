package wire

import (
	"context"
	"maps"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Label is the public identity a wire stream's messages must carry.
type Label struct {
	// Wire is the hidden provider name the host stamps on messages.
	Wire string
	// Provider and API replace it on every message, partial and error.
	Provider, API string
}

// Relay forwards inner's events with messages relabeled to the public provider, and lets
// finalize (when non-nil) rewrite the final assistant message of a done event or End result.
// The relay stops when signal ends; the inner stream is cancelled by the same signal through
// its fetch exchange.
func Relay(signal context.Context, inner *sdk.ModelEventStream, label Label, finalize func(message map[string]any) map[string]any) *sdk.ModelEventStream {
	if signal == nil {
		signal = context.Background()
	}
	if finalize == nil {
		finalize = func(m map[string]any) map[string]any { return m }
	}
	out := sdk.CreateAssistantMessageEventStream()
	go func() {
		terminal := false
		for event := range inner.Events(signal) {
			t, _ := event["type"].(string)
			next := maps.Clone(event)
			for _, key := range []string{"partial", "message", "error"} {
				if m, ok := next[key].(map[string]any); ok {
					next[key] = label.apply(m)
				}
			}
			if t == "done" || t == "error" {
				terminal = true
				if m, ok := next["message"].(map[string]any); ok && t == "done" {
					next["message"] = finalize(m)
				}
			}
			out.Push(next)
		}
		// Events also closes when the inner stream ends through End, without a terminal event.
		if terminal || signal.Err() != nil {
			return
		}
		result := inner.Result()
		if result != nil {
			result = label.apply(result)
			if result["role"] == "assistant" && result["stopReason"] != "error" && result["stopReason"] != "aborted" {
				result = finalize(result)
			}
		}
		out.End(result)
	}()
	return out
}

func (l Label) apply(m map[string]any) map[string]any {
	if m["provider"] != l.Wire {
		return m
	}
	out := maps.Clone(m)
	out["provider"] = l.Provider
	if l.API != "" {
		out["api"] = l.API
	}
	return out
}
