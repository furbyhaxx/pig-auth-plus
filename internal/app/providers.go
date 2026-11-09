package app

import (
	"fmt"
	"os"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/antigravity"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/claude"
	"github.com/furbyhaxx/pig-auth-plus/internal/providers/synthetic"
	"github.com/furbyhaxx/pig-auth-plus/internal/wire"
)

// providers are registered once per process; sessions bind the wire runtime and refresh catalogs.
type providers struct {
	runtime     *wire.Runtime
	claude      *claude.Provider
	synthetic   *synthetic.Provider
	antigravity *antigravity.Provider
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
	native := ps.claude.Native()
	if err := ext.RegisterNativeProvider(native); err != nil {
		return nil, fmt.Errorf("claude provider: %w", err)
	}
	syntheticCatalog, err := synthetic.NewCatalog()
	if err != nil {
		return nil, fmt.Errorf("synthetic catalog: %w", err)
	}
	ps.synthetic = &synthetic.Provider{Catalog: syntheticCatalog, Runtime: ps.runtime, Count: synthetic.UnitCounter(syntheticCatalog)}
	if err := ext.RegisterNativeProvider(ps.synthetic.Native()); err != nil {
		return nil, fmt.Errorf("synthetic provider: %w", err)
	}
	agCatalog, err := antigravity.NewCatalog()
	if err != nil {
		return nil, fmt.Errorf("antigravity catalog: %w", err)
	}
	ps.antigravity = &antigravity.Provider{
		Catalog: agCatalog, Auth: &antigravity.Auth{Endpoints: antigravity.DefaultEndpoints},
		Projects: &antigravity.Projects{}, Sessions: &antigravity.Sessions{}, Runtime: ps.runtime,
	}
	if err := ext.RegisterNativeProvider(ps.antigravity.Native()); err != nil {
		return nil, fmt.Errorf("antigravity provider: %w", err)
	}
	return ps, nil
}

// start binds the session and reports a malformed Claude version override.
func (ps *providers) start(ctx sdk.Context) {
	ps.runtime.Bind(&ctx)
	// The CLI re-derives its project per run; a new session does the same.
	ps.antigravity.Projects.Reset()
	if w := ps.claude.Shaper.VersionWarning(); w != "" {
		ctx.Notify(w, "warning")
	}
}

func (ps *providers) stop() { ps.runtime.Bind(nil) }
