package alias

import (
	"errors"
	"slices"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

func model(provider, id string, ctx float64, reasoning bool) map[string]any {
	return map[string]any{"provider": provider, "id": id, "name": id + " name", "contextWindow": ctx, "maxTokens": float64(1000),
		"reasoning": reasoning, "input": []any{"text", "image"}}
}

type fakeHost struct {
	physical   []map[string]any
	registered map[string]sdk.VirtualModel
	failList   bool
	failReg    bool
	unregCalls []string
}

func newHost(m ...map[string]any) *fakeHost {
	return &fakeHost{physical: m, registered: map[string]sdk.VirtualModel{}}
}

func (h *fakeHost) Register(v sdk.VirtualModel) error {
	if h.failReg {
		return errors.New("refused")
	}
	h.registered[v.Provider+"/"+v.ID] = v
	return nil
}

func (h *fakeHost) Unregister(p, id string) {
	delete(h.registered, p+"/"+id)
	h.unregCalls = append(h.unregCalls, p+"/"+id)
}

// Physical includes registered virtual models, as the real host lists them.
func (h *fakeHost) Physical() ([]map[string]any, error) {
	if h.failList {
		return nil, errors.New("host down")
	}
	out := slices.Clone(h.physical)
	for _, v := range h.registered {
		out = append(out, model(v.Provider, v.ID, 0, false))
	}
	return out, nil
}

func (h *fakeHost) find(_ sdk.Context, p, id string) (map[string]any, error) {
	for _, m := range h.physical {
		if m["provider"] == p && m["id"] == id {
			return m, nil
		}
	}
	return nil, nil
}

func TestPlanInheritsFromSourceAndOverridesLimits(t *testing.T) {
	phys := []map[string]any{model("claude", "claude-opus-5", 1_000_000, true)}
	valid, problems := Plan([]config.Alias{
		{Provider: "claude", Source: "claude-opus-5", ID: "fast"},
		{Provider: "claude", Source: "claude-opus-5", ID: "small", Name: "Small", ContextWindow: 200000},
	}, phys)
	if len(problems) != 0 || len(valid) != 2 {
		t.Fatalf("%v %v", valid, problems)
	}
	if valid[0].ContextWindow != 1_000_000 || valid[0].Name != "claude-opus-5 name (fast)" || !slices.Contains(valid[0].Levels, "high") {
		t.Fatalf("inheritance: %+v", valid[0])
	}
	if valid[1].ContextWindow != 200000 || valid[1].MaxTokens != 1000 || valid[1].Name != "Small" {
		t.Fatalf("override: %+v", valid[1])
	}
}

func TestPlanRejectsBadAliases(t *testing.T) {
	phys := []map[string]any{model("claude", "a", 1, false), model("claude", "b", 1, false)}
	_, problems := Plan([]config.Alias{
		{Provider: "claude", Source: "a", ID: "b"},   // collides with a model
		{Provider: "claude", Source: "zzz", ID: "x"}, // missing source
		{Provider: "claude", Source: "a", ID: "a"},   // own source
		{Provider: "claude", Source: "a", ID: "ok"},  // fine
		{Provider: "claude", Source: "b", ID: "ok"},  // duplicate id
		{Provider: "", Source: "a", ID: "y"},         // incomplete
	}, phys)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"collides", "not in the catalog", "its own source", "defined twice", "needs a provider"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
}

func TestSyncRegistersReplacesAndRemoves(t *testing.T) {
	h := newHost(model("claude", "opus", 1000, true))
	r := NewRegistry(h, h.find)
	cfg := []config.Alias{{Provider: "claude", Source: "opus", ID: "fast"}}
	if problems, err := r.Sync(cfg); err != nil || len(problems) != 0 || h.registered["claude/fast"].ID != "fast" {
		t.Fatalf("%v %v", problems, err)
	}
	// A second sync with the alias already listed by the host must not collide with itself.
	if problems, _ := r.Sync(cfg); len(problems) != 0 {
		t.Fatalf("self collision: %v", problems)
	}
	cfg[0].ContextWindow = 5
	r.Sync(cfg)
	if h.registered["claude/fast"].ContextWindow != 5 {
		t.Fatal("a changed alias must be re-registered")
	}
	r.Sync(nil)
	if len(h.registered) != 0 || len(r.Registered()) != 0 {
		t.Fatal("a removed alias must be unregistered")
	}
}

func TestSyncKeepsAliasesWhenCatalogUnreadable(t *testing.T) {
	h := newHost(model("claude", "opus", 1000, false))
	r := NewRegistry(h, h.find)
	r.Sync([]config.Alias{{Provider: "claude", Source: "opus", ID: "fast"}})
	h.failList = true
	if _, err := r.Sync(nil); err == nil {
		t.Fatal("an unreadable catalog is an error")
	}
	if len(h.registered) != 1 {
		t.Fatal("nothing may change while the catalog is unreadable")
	}
}

func TestRegistrationRefusalIsReported(t *testing.T) {
	h := newHost(model("claude", "opus", 1000, false))
	h.failReg = true
	r := NewRegistry(h, h.find)
	problems, _ := r.Sync([]config.Alias{{Provider: "claude", Source: "opus", ID: "fast"}})
	if len(problems) != 1 || !strings.Contains(problems[0], "refused") || len(r.Registered()) != 0 {
		t.Fatalf("%v", problems)
	}
}

func TestRouteSendsRequestToSourceWithClampedLevel(t *testing.T) {
	src := model("claude", "opus", 1000, true)
	h := newHost(src)
	r := NewRegistry(h, h.find)
	r.Sync([]config.Alias{{Provider: "claude", Source: "opus", ID: "fast"}})
	route, err := h.registered["claude/fast"].Route(sdk.Context{}, sdk.ModelRouteRequest{ThinkingLevel: "xhigh"})
	if err != nil || route.Model["id"] != "opus" || route.ThinkingLevel != "high" {
		t.Fatalf("%+v %v", route, err)
	}
	h.physical = nil
	if _, err := h.registered["claude/fast"].Route(sdk.Context{}, sdk.ModelRouteRequest{}); err == nil {
		t.Fatal("a vanished source must fail the request, not route to nothing")
	}
}

func TestCloseUnregistersAll(t *testing.T) {
	h := newHost(model("claude", "opus", 1000, false))
	r := NewRegistry(h, h.find)
	r.Sync([]config.Alias{{Provider: "claude", Source: "opus", ID: "a"}, {Provider: "claude", Source: "opus", ID: "b"}})
	r.Close()
	if len(h.registered) != 0 {
		t.Fatal("Close must unregister every alias")
	}
}

func TestEarlyAliasesSurviveFirstSyncAndRoute(t *testing.T) {
	h := newHost(model("claude", "opus", 1000, true))
	cfg := []config.Alias{{Provider: "claude", Source: "opus", ID: "fast"}, {Provider: "claude", Source: "claude", ID: "claude"}}
	early := Early(cfg, h.find)
	if len(early) != 1 || early[0].ID != "fast" || len(early[0].ThinkingLevels) == 0 {
		t.Fatalf("%+v", early)
	}
	route, err := early[0].Route(sdk.Context{}, sdk.ModelRouteRequest{ThinkingLevel: "low"})
	if err != nil || route.Model["id"] != "opus" {
		t.Fatalf("%+v %v", route, err)
	}
	// The host already lists the early alias when the session starts.
	h.registered["claude/fast"] = early[0]
	r := NewRegistry(h, h.find)
	r.Seed(early)
	if problems, err := r.Sync(cfg[:1]); err != nil || len(problems) != 0 {
		t.Fatalf("an early alias must not collide with itself: %v %v", problems, err)
	}
	if got := r.Registered(); len(got) != 1 || got[0].ContextWindow != 1000 {
		t.Fatalf("the first sync must replace the early registration with full data: %+v", got)
	}
}
