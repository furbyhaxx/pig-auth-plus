// Package app wires pig-auth-plus features to the PiG SDK. It owns the per-session state.
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/migrate"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/session"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

// startupTimeout bounds file work during session start.
const startupTimeout = 10 * time.Second

// state is everything one session owns. It is replaced, never mutated across sessions.
type state struct {
	host     sdk.Context
	paths    paths.Paths
	settings *config.Manager
	group    *session.Group
	quotas   *quota.Engine
	tokens   *tokens.Tracker
	line     *lineView
	aliases  *aliasSync
}

// App holds the current session's state.
type App struct {
	early     []sdk.VirtualModel
	mu        sync.Mutex
	cur       *state
	providers *providers
}

// Register wires lifecycle handlers into ext.
func Register(ext *sdk.Extension) {
	a := &App{}
	ps, err := registerProviders(ext, a)
	if err != nil {
		panic("pig-auth-plus: " + err.Error())
	}
	a.providers = ps
	a.early = registerEarlyAliases(ext)
	a.registerCommands(ext)
	ext.OnSessionStart(a.start)
	ext.OnSessionShutdown(a.shutdown)
	ext.OnEvent(sdk.EventBeforeProviderRequest, a.onProviderRequest)
	ext.OnEvent(sdk.EventAfterProviderResponse, a.onProviderResponse)
	ext.OnEvent(sdk.EventTurnEnd, a.onTurnEnd)
	ext.OnEvent(sdk.EventModelSelect, a.resyncAliases)
	for event, reason := range map[string]tokens.Reason{
		sdk.EventModelSelect:         tokens.ReasonModelSelect,
		sdk.EventThinkingLevelSelect: tokens.ReasonThinkingLevelSelect,
		sdk.EventMessageEnd:          tokens.ReasonMessageEnd,
		sdk.EventTurnEnd:             tokens.ReasonTurnEnd,
		sdk.EventSessionCompact:      tokens.ReasonSessionCompact,
		sdk.EventSessionTree:         tokens.ReasonSessionTree,
	} {
		ext.OnEvent(event, a.tokenEvent(reason))
	}
}

func (a *App) start(ctx sdk.Context, _ map[string]any) (any, error) {
	a.stop()
	if msg, conflict := detectConflict(ctx); conflict {
		ctx.Notify(msg, "error")
		return nil, nil
	}
	agentDir, err := sdk.AgentDir()
	if err != nil {
		return nil, fmt.Errorf("pig-auth-plus: %w", err)
	}
	p, err := paths.Resolve(agentDir, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("pig-auth-plus: %w", err)
	}
	st := &state{host: ctx, paths: p, settings: config.NewManager(p.UserConfig()), group: session.NewGroup()}
	_ = st.settings.Load() // reported through snapshot warnings
	work, cancel := context.WithTimeout(st.group.Context(), startupTimeout)
	defer cancel()
	res, err := migrate.ImportLegacySettings(work, p, st.settings)
	switch {
	case err != nil:
		ctx.Notify("pig-auth-plus: "+err.Error(), "warning")
	case len(res.Imported) > 0:
		ctx.Notify(fmt.Sprintf("pig-auth-plus: imported %d settings from auth-plus.json into auth-plus.toml", len(res.Imported)), "info")
	}
	for _, w := range res.Warnings {
		ctx.Notify("pig-auth-plus import: "+w, "warning")
	}
	if w := st.settings.Snapshot().Warnings; len(w) > 0 {
		ctx.Notify("pig-auth-plus settings: "+strings.Join(w, "; "), "warning")
	}
	a.mu.Lock()
	a.cur = st
	a.mu.Unlock()
	a.providers.start(ctx)
	a.startTokens(st)
	if err := a.startQuotas(st); err != nil {
		ctx.Notify("pig-auth-plus quotas: "+err.Error(), "warning")
		return nil, nil
	}
	a.startLine(st)
	a.startAliases(st, a.early)
	return nil, nil
}

func (a *App) session() *state {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur
}

// settings is the current session's resolved settings, or the built-in defaults between sessions.
func (a *App) settings() config.Settings {
	a.mu.Lock()
	st := a.cur
	a.mu.Unlock()
	if st == nil {
		return config.Defaults()
	}
	return st.settings.Snapshot().Settings
}

func (a *App) shutdown(sdk.Context, map[string]any) (any, error) {
	a.stop()
	return nil, nil
}

func (a *App) stop() {
	a.mu.Lock()
	st := a.cur
	a.cur = nil
	a.mu.Unlock()
	if st != nil {
		if st.line != nil {
			st.line.clear(st.line.placement())
		}
		st.group.Close()
		if st.aliases != nil {
			st.aliases.reg.Close()
		}
		if st.tokens != nil {
			st.tokens.Close()
		}
		if st.quotas != nil {
			st.quotas.Stop()
		}
	}
	if a.providers != nil {
		a.providers.stop()
	}
}

func detectConflict(ctx sdk.Context) (string, bool) {
	infos, err := ctx.GetCommands()
	if err != nil {
		return "", false
	}
	own, _ := ctx.ExtensionSourceInfo()
	cmds := make([]session.Command, 0, len(infos))
	for _, c := range infos {
		if c.Source == "extension" {
			cmds = append(cmds, session.Command{Name: c.Name, Source: c.SourceInfo.Source, Location: location(c.SourceInfo)})
		}
	}
	return session.Conflict(cmds, location(own))
}

// location identifies where an extension was loaded from: its package root, else its entry path.
func location(info sdk.SourceInfo) string {
	if info.BaseDir != "" {
		return info.BaseDir
	}
	return info.Path
}

// resyncAliases retries aliases whose source model appeared after the last sync, such as a
// refreshed provider catalog.
func (a *App) resyncAliases(sdk.Context, map[string]any) (any, error) {
	st := a.session()
	if st == nil || st.aliases == nil {
		return nil, nil
	}
	aliases := st.settings.Snapshot().Aliases
	st.group.Go(func(context.Context) { st.aliases.sync(aliases) })
	return nil, nil
}
