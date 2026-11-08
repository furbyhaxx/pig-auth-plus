package wire

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Relay forwards inner's events and lets finalize rewrite the final assistant message of a
// done event. Error events pass through unchanged. The relay stops when signal ends; the inner
// stream is cancelled by the same signal through its fetch exchange.
func Relay(signal context.Context, inner *sdk.ModelEventStream, finalize func(message map[string]any) map[string]any) *sdk.ModelEventStream {
	if signal == nil {
		signal = context.Background()
	}
	out := sdk.CreateAssistantMessageEventStream()
	go func() {
		terminal := false
		for event := range inner.Events(signal) {
			if t, _ := event["type"].(string); t == "done" || t == "error" {
				terminal = true
				if message, ok := event["message"].(map[string]any); ok && t == "done" {
					next := make(map[string]any, len(event))
					for k, v := range event {
						next[k] = v
					}
					next["message"] = finalize(message)
					event = next
				}
			}
			out.Push(event)
		}
		// Events also closes when the inner stream ends through End, without a terminal event.
		if terminal || signal.Err() != nil {
			return
		}
		result := inner.Result()
		if result != nil && result["role"] == "assistant" && result["stopReason"] != "error" && result["stopReason"] != "aborted" {
			result = finalize(result)
		}
		out.End(result)
	}()
	return out
}
