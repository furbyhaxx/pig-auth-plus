package app

import (
	"context"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/alias"
	"github.com/furbyhaxx/pig-auth-plus/internal/config"
)

// aliasHost adapts the SDK context to the alias registry.
type aliasHost struct{ ctx sdk.Context }

func (h aliasHost) Register(v sdk.VirtualModel) error { return h.ctx.RegisterVirtualModel(v) }
func (h aliasHost) Unregister(provider, id string)    { h.ctx.UnregisterVirtualModel(provider, id) }
func (h aliasHost) Physical() ([]map[string]any, error) {
	return h.ctx.ModelRegistry().GetAll()
}
func (h aliasHost) Find(provider, id string) (map[string]any, error) {
	return h.ctx.ModelRegistry().Find(provider, id)
}

// aliasSync keeps registered aliases in step with settings and reports problems once.
type aliasSync struct {
	reg    *alias.Registry
	notify func(string)

	mu       sync.Mutex
	reported string
}

func (a *App) startAliases(st *state) {
	as := &aliasSync{reg: alias.NewRegistry(aliasHost{st.host}), notify: func(m string) { st.host.Notify(m, "warning") }}
	st.aliases = as
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
