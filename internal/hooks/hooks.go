// Package hooks rewrites outgoing provider payloads for the openai verbosity and claude
// cache retention settings. Functions are pure; they return nil when the payload is untouched.
package hooks

import "slices"

// Model is the selected model as the hooks need it.
type Model struct {
	Provider, ID, API string
	// SamplingParams and Compat are the model's catalog fields, when present.
	SamplingParams, Compat map[string]any
}

// Verbosity sets text.verbosity on an OpenAI Responses payload. A model's own
// samplingParams.text.verbosity wins over the setting.
func Verbosity(payload any, m Model, verbosity string, providers []string) map[string]any {
	effective := verbosity
	if text, ok := m.SamplingParams["text"].(map[string]any); ok {
		switch v, _ := text["verbosity"].(string); v {
		case "low", "medium", "high":
			effective = v
		}
	}
	body, ok := payload.(map[string]any)
	if effective == "default" || effective == "" || !ok || !slices.Contains(providers, m.Provider) ||
		(m.API != "openai-responses" && m.API != "openai-codex-responses") || body["model"] != m.ID {
		return nil
	}
	if _, ok := body["input"].([]any); !ok {
		return nil
	}
	text := map[string]any{}
	switch t := body["text"].(type) {
	case nil:
	case map[string]any:
		for k, v := range t {
			text[k] = v
		}
	default:
		return nil
	}
	text["verbosity"] = effective
	return with(body, "text", text)
}

// CacheRetention sets every ephemeral cache breakpoint of an Anthropic Messages payload to
// the 5-minute ("short") or 1-hour ("long") lifetime. Breakpoints move together because
// Anthropic rejects a 1-hour breakpoint after a 5-minute one.
func CacheRetention(payload any, m Model, retention string, providers []string) map[string]any {
	body, ok := payload.(map[string]any)
	if (retention != "short" && retention != "long") || !ok || !slices.Contains(providers, m.Provider) ||
		m.API != "anthropic-messages" || body["model"] != m.ID {
		return nil
	}
	if retention == "long" && m.Compat["supportsLongCacheRetention"] == false {
		return nil
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	changed := false
	for _, key := range []string{"system", "tools"} {
		if list, c := retimedList(body[key], retention); c {
			out[key], changed = list, true
		}
	}
	next := make([]any, len(messages))
	for i, raw := range messages {
		next[i] = raw
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if content, c := retimedList(msg["content"], retention); c {
			next[i], changed = with(msg, "content", content), true
		}
	}
	if !changed {
		return nil
	}
	out["messages"] = next
	return out
}

func retimedList(list any, retention string) (any, bool) {
	items, ok := list.([]any)
	if !ok {
		return list, false
	}
	next := make([]any, len(items))
	changed := false
	for i, item := range items {
		next[i] = item
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		control, ok := block["cache_control"].(map[string]any)
		if !ok || control["type"] != "ephemeral" {
			continue
		}
		ttl, has := control["ttl"]
		if retention == "long" && ttl == "1h" || retention == "short" && !has {
			continue
		}
		c := map[string]any{}
		for k, v := range control {
			c[k] = v
		}
		delete(c, "ttl")
		if retention == "long" {
			c["ttl"] = "1h"
		}
		next[i], changed = with(block, "cache_control", c), true
	}
	if !changed {
		return list, false
	}
	return next, true
}

func with(m map[string]any, key string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, x := range m {
		out[k] = x
	}
	out[key] = v
	return out
}
