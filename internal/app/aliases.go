package app

import (
	"context"
	"os"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/alias"
	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
)

// aliasHost adapts the SDK context to the alias registry.
type aliasHost struct{ ctx sdk.Context }

func (h aliasHost) Register(v sdk.VirtualModel) error { return h.ctx.RegisterVirtualModel(v) }
func (h aliasHost) Unregister(provider, id string)    { h.ctx.UnregisterVirtualModel(provider, id) }
func (h aliasHost) Physical() ([]map[string]any, error) {
	return h.ctx.ModelRegistry().GetAll()
}

func findModel(ctx sdk.Context, provider, id string) (map[string]any, error) {
	return ctx.ModelRegistry().Find(provider, id)
}

// registerEarlyAliases declares the user file's aliases while the extension loads, so that
// --model and session restore, which run before session start, already know them.
func registerEarlyAliases(ext *sdk.Extension) []sdk.VirtualModel {
	dir, err := sdk.AgentDir()
	if err != nil {
		return nil
	}
	p, err := paths.Resolve(dir, os.Getenv)
	if err != nil {
		return nil
	}
	m := config.NewManager(p.UserConfig())
	if err := m.Load(); err != nil {
		return nil
	}
	early := alias.Early(m.Snapshot().Aliases, findModel)
	var ok []sdk.VirtualModel
	for _, v := range early {
		if ext.RegisterVirtualModel(v) == nil {
			ok = append(ok, v)
		}
	}
	return ok
}

// aliasSync keeps registered aliases in step with settings and reports problems once.
type aliasSync struct {
	reg    *alias.Registry
	notify func(string)

	mu       sync.Mutex
	reported string
}

func (a *App) startAliases(st *state, early []sdk.VirtualModel) {
	as := &aliasSync{reg: alias.NewRegistry(aliasHost{st.host}, findModel), notify: func(m string) { st.host.Notify(m, "warning") }}
	st.aliases = as
	as.reg.Seed(early)
	run := func(s config.Settings) {
		st.group.Go(func(context.Context) { as.sync(s.Aliases) })
	}
	st.settings.Subscribe(func(snap *config.Snapshot) { run(snap.Settings) })
	run(st.settings.Snapshot().Settings)
}

// sync registers the aliases and tells the user about rejected ones, once per distinct set.
func (s *aliasSync) sync(aliases []config.Alias) {
	problems, err := s.reg.Sync(aliases)
	if err != nil {
		return
	}
	msg := strings.Join(problems, "\n")
	s.mu.Lock()
	changed := msg != s.reported
	s.reported = msg
	s.mu.Unlock()
	if changed && msg != "" {
		s.notify("pig-auth-plus aliases:\n" + msg)
	}
}
