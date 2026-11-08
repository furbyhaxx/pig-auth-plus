package app

import (
	"fmt"
	"os"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/claude"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// providers are registered once per process; sessions bind the wire runtime and refresh catalogs.
type providers struct {
	runtime *wire.Runtime
	claude  *claude.Provider
}

func registerProviders(ext *sdk.Extension, a *App) (*providers, error) {
	catalog, err := claude.NewCatalog()
	if err != nil {
		return nil, fmt.Errorf("claude catalog: %w", err)
	}
	resolve := sync.OnceValues(func() (paths.Paths, error) {
		agentDir, err := sdk.AgentDir()
		if err != nil {
			return paths.Paths{}, err
		}
		return paths.Resolve(agentDir, os.Getenv)
	})
	wireDir := func() (string, error) { p, err := resolve(); return p.Wire(), err }
	stateDir := func() (string, error) { p, err := resolve(); return p.StateDir, err }
	ps := &providers{runtime: wire.NewRuntime(wireDir)}
	ps.claude = &claude.Provider{
		Catalog: catalog,
		Auth:    &claude.Auth{Endpoints: claude.DefaultEndpoints},
		Shaper:  claude.NewShaper(stateDir, os.Getenv, func() []config.ReplacePattern { return a.settings().ReplacePatterns }),
		Runtime: ps.runtime,
	}
	native, err := ps.claude.Native()
	if err != nil {
		return nil, fmt.Errorf("claude provider: %w", err)
	}
	if err := ext.RegisterNativeProvider(native); err != nil {
		return nil, fmt.Errorf("claude provider: %w", err)
	}
	return ps, nil
}

// start binds the session and republishes catalogs that differ from the running host's.
func (ps *providers) start(ctx sdk.Context) {
	ps.runtime.Bind(&ctx)
	anthropic, err := ctx.ModelRegistry().GetModelsOfType("chat", "anthropic")
	if err != nil || !ps.claude.Catalog.Update(anthropic) {
		return
	}
	force := true
	if _, err := ctx.ModelRegistry().Refresh(sdk.ModelsRefreshOptions{Providers: []string{claude.ProviderID}, Force: &force}); err != nil {
		ctx.Notify("pig-auth-plus: claude catalog refresh: "+err.Error(), "warning")
	}
	if w := ps.claude.Shaper.VersionWarning(); w != "" {
		ctx.Notify(w, "warning")
	}
}

func (ps *providers) stop() { ps.runtime.Bind(nil) }
