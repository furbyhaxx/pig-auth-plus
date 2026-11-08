package app

import (
	"context"
	"encoding/json"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/providers/claude"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

// newTokenEngine picks the best counter per provider: Claude counts complete requests natively,
// every other route anchors on provider usage and estimates the tail.
func (a *App) newTokenEngine(st *state) *tokens.Engine {
	native := &claude.Counter{
		Provider: a.providers.claude,
		Token: func(ctx context.Context) (string, error) {
			auth, err := providerAuth(st.host, claude.ProviderID)
			if err != nil || auth == nil {
				return "", err
			}
			return auth.Token, nil
		},
		Model: func(id string) (map[string]any, error) { return st.host.ModelRegistry().Find(claude.ProviderID, id) },
	}
	return &tokens.Engine{For: func(provider, model string) tokens.Counter {
		if provider == claude.ProviderID {
			return native
		}
		return tokens.EstimateCounter{Ratios: tokens.RatiosFor(provider, model)}
	}}
}

// startTokens builds the session's tracker and subscribes it to the session events that change
// the context.
func (a *App) startTokens(st *state) {
	st.tokens = tokens.NewTracker(a.newTokenEngine(st), func(context.Context) (tokens.Input, bool) {
		return gatherTokenInput(st.host)
	}, tokens.DefaultDebounce)
	st.tokens.Schedule(tokens.ReasonSessionStart, tokenKey(st.host, true))
}

func (a *App) tokenEvent(reason tokens.Reason) sdk.EventFunc {
	return func(ctx sdk.Context, _ map[string]any) (any, error) {
		st := a.session()
		if st == nil || st.tokens == nil {
			return nil, nil
		}
		st.tokens.Schedule(reason, tokenKey(ctx, reason == tokens.ReasonSessionTree))
		return nil, nil
	}
}

// tokenKey names what a report describes. The branch costs a host read, so only events that can
// change it ask.
func tokenKey(ctx sdk.Context, withBranch bool) tokens.Key {
	key := tokens.Key{Provider: ctx.ModelProvider(), Model: ctx.Model()}
	if !withBranch {
		return key
	}
	sm := ctx.SessionManager()
	id, _ := sm.GetSessionID()
	if leaf, err := sm.GetLeafID(); err == nil && leaf != nil {
		key.Branch = id + "/" + *leaf
	} else {
		key.Branch = id
	}
	return key
}

// gatherTokenInput reads the live session state a report is computed from.
func gatherTokenInput(ctx sdk.Context) (tokens.Input, bool) {
	provider, model := ctx.ModelProvider(), ctx.Model()
	if provider == "" || model == "" {
		return tokens.Input{}, false
	}
	sm := ctx.SessionManager()
	projection, err := sm.BuildSessionProjection()
	if err != nil {
		return tokens.Input{}, false
	}
	branch, err := sm.GetBranch(nil)
	if err != nil {
		return tokens.Input{}, false
	}
	in := tokens.Input{Provider: provider, Model: model, Messages: projection.Messages, ThinkingLevel: projection.ThinkingLevel}
	for _, e := range projection.Entries {
		id, _ := e.SourceEntry["id"].(string)
		in.Entries = append(in.Entries, tokens.ProjectedEntry{SourceID: id, Messages: len(e.Messages)})
	}
	for _, e := range branch {
		id, _ := e["id"].(string)
		typ, _ := e["type"].(string)
		in.Branch = append(in.Branch, tokens.BranchEntry{ID: id, Type: typ})
	}
	if found, err := ctx.ModelRegistry().Find(provider, model); err == nil && found != nil {
		if w, ok := found["contextWindow"].(float64); ok {
			in.ContextWindow = int(w)
		}
	}
	if usage, err := ctx.GetContextUsage(); err == nil && usage != nil {
		in.HostTokens = usage.Tokens
		if in.ContextWindow == 0 {
			in.ContextWindow = usage.ContextWindow
		}
	}
	if prompt, err := ctx.GetSystemPrompt(); err == nil {
		in.LiveSystem = prompt
	}
	in.LiveTools = liveTools(ctx)
	return in, true
}

func liveTools(ctx sdk.Context) []tokens.ToolSchema {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return nil
	}
	all, err := ctx.GetAllTools()
	if err != nil {
		return nil
	}
	on := make(map[string]bool, len(active))
	for _, n := range active {
		on[n] = true
	}
	var out []tokens.ToolSchema
	for _, t := range all {
		if !on[t.Name] {
			continue
		}
		var params any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &params)
		}
		out = append(out, tokens.ToolSchema{Name: t.Name, Description: t.Description, Parameters: params})
	}
	return out
}
