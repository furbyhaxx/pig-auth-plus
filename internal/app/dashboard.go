package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/resetflow"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	uimodels "github.com/furbyhaxx/pig-auth-plus/internal/ui/models"
	uiproviders "github.com/furbyhaxx/pig-auth-plus/internal/ui/providers"
	uiquotas "github.com/furbyhaxx/pig-auth-plus/internal/ui/quotas"
	uisettings "github.com/furbyhaxx/pig-auth-plus/internal/ui/settings"
	uitokens "github.com/furbyhaxx/pig-auth-plus/internal/ui/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

const (
	defaultWidth = 80
	defaultRows  = 24
	// reservedRows is what the footer and the quota widget keep for themselves; the panel is
	// drawn above them and its hint row is lost when it claims the whole terminal.
	reservedRows = 6
)

// dashboard is one open /auth panel; it lives until the panel closes.
type dashboard struct {
	a      *App
	st     *state
	frame  *frame.Frame
	provs  *uiproviders.Tab
	models *uimodels.Tab
	sett   *uisettings.Tab
	quotas *uiquotas.Tab
	tokens *uitokens.Tab

	mu         sync.Mutex
	counting   context.CancelFunc
	rows       []uiproviders.Row
	catalog    []uimodels.Model
	catalogErr string
}

// openDashboard shows the /auth panel on the tab named by section. solo starts the Quotas tab
// on one provider.
func (a *App) openDashboard(ctx sdk.Context, section, solo, key string) error {
	st := a.session()
	if st == nil {
		ctx.Notify("pig-auth-plus is not active in this session.", "warning")
		return nil
	}
	if section != "providers" && section != "quotas" && section != "tokens" && section != "models" && section != "settings" {
		ctx.Notify("The "+section+" tab is not available yet.", "info")
		return nil
	}
	d := &dashboard{a: a, st: st}
	d.provs = uiproviders.New(d.providerRows, func() { d.rereadProviders(ctx) })
	d.quotas = uiquotas.New(st.quotas.View, a.display, uiquotas.Actions{
		Refresh:   d.refresh,
		Placement: func() { d.async(d.choosePlacement) },
		Reset:     func(id string) { d.async(func(c context.Context) { d.reset(c, id) }) },
	}, solo)
	d.tokens = uitokens.New(st.tokens.Latest, a.tokenDisplay, func() { d.count(true) })
	d.models = uimodels.New(d.catalogRows, d.catalogError, uimodels.Actions{
		Alias: d.aliasDefinition,
		SaveAlias: func(old *uimodels.Model, a uimodels.Alias) {
			d.async(func(c context.Context) { d.saveAlias(c, old, a) })
		},
		RemoveAlias: func(m uimodels.Model) { d.async(func(c context.Context) { d.removeAlias(c, m) }) },
		SaveOverride: func(m uimodels.Model, ops []modelsfile.Op) {
			d.async(func(c context.Context) { d.saveOverride(c, ctx, m, ops) })
		},
	})
	d.sett = uisettings.New(st.settings.Snapshot, uisettings.Actions{
		Save:    func(edits []config.Edit) { d.async(func(c context.Context) { d.saveSettings(c, edits) }) },
		Preview: d.previewTemplate,
		Known:   d.knownProviders,
	}, key)
	level, lerr := ctx.GetThinkingLevel()
	if lerr != nil {
		level = "" // the rules then use the border color
	}
	d.frame = frame.New(func() frame.Env {
		w, h := ctx.Width(), ctx.Height()
		if w <= 0 {
			w = defaultWidth
		}
		if h <= 0 {
			h = defaultRows
		}
		return frame.Env{Width: w, Rows: max(h-reservedRows, 8), Theme: ctx.UITheme(), Level: level}
	}, section, d.provs, d.quotas, d.tokens, d.models, d.sett)
	stop := st.quotas.OnChange(d.frame.Invalidate)
	defer stop()
	stopTokens := st.tokens.OnChange(func(*tokens.Report, tokens.Reason) {
		d.frame.Invalidate()
		d.count(false)
	})
	defer stopTokens()
	d.count(true)
	d.rereadProviders(ctx)
	d.rereadCatalog(ctx)
	_, err := ctx.Custom(d.frame, nil)
	return err
}

func (a *App) tokenDisplay() uitokens.Display {
	s := a.settings()
	return uitokens.Display{WarningPercent: s.TokensWarningPercent, ErrorPercent: s.TokensErrorPercent}
}

// count recounts the breakdown in the background, and the report too when refresh is set;
// overlapping requests collapse into the newest.
func (d *dashboard) count(refresh bool) {
	d.mu.Lock()
	if d.counting != nil {
		d.counting()
	}
	d.mu.Unlock()
	d.async(func(ctx context.Context) {
		run, cancel := context.WithCancel(ctx)
		defer cancel()
		d.mu.Lock()
		d.counting = cancel
		d.mu.Unlock()
		d.tokens.SetBreakdown(uitokens.Breakdown{Loading: true})
		d.frame.Invalidate()
		if refresh {
			d.st.tokens.Refresh(run, tokens.ReasonRequest)
		}
		parts, ok := d.st.tokens.Breakdown(run)
		if run.Err() != nil {
			return
		}
		d.tokens.SetBreakdown(uitokens.Breakdown{Parts: parts, Failed: !ok})
		d.frame.Invalidate()
	})
}

func (a *App) display() uiquotas.Display {
	s := a.settings()
	return uiquotas.Display{
		PercentUsed:       s.PercentDisplay == "used",
		HideSignedOut:     s.HideSignedOut,
		CreditHealthyUSD:  float64(s.CreditHealthyUSD),
		CreditCriticalUSD: float64(s.CreditCriticalUSD),
	}
}

// async runs fn on the session's goroutine group so a dialog never blocks the panel's queue and
// nothing outlives the session.
func (d *dashboard) async(fn func(ctx context.Context)) {
	d.st.group.Go(fn)
}

func (d *dashboard) notice(text string, warn bool) {
	d.quotas.SetNotice(text, warn)
	d.frame.Invalidate()
}

// refresh forces one provider, or all enabled ones, in the background.
func (d *dashboard) refresh(id string) {
	d.async(func(ctx context.Context) {
		ids := []string{id}
		if id == "" {
			ids = ids[:0]
			for _, s := range d.st.quotas.Sources() {
				ids = append(ids, s.ID)
			}
		}
		var held []string
		for _, id := range ids {
			if err := d.st.quotas.Refresh(ctx, id, quota.RefreshOptions{Force: true}); err != nil && ctx.Err() == nil {
				held = append(held, fmt.Sprintf("%s: %v", id, err))
			}
		}
		if len(held) > 0 {
			d.notice("Refresh: "+strings.Join(held, "; "), true)
		}
	})
}

type placementChoice struct{ label, value string }

var placements = []placementChoice{
	{"Above the editor", placeAbove},
	{"Below the editor (first)", placeBelow},
	{"Footer status (last rows)", placeBelowLast},
	{"Hidden (refresh continues)", placeOff},
}

type scopeChoice struct {
	label string
	scope config.Scope
}

var scopes = []scopeChoice{
	{"This session only", config.ScopeSession},
	{"User file (auth-plus.toml)", config.ScopeUser},
}

// choosePlacement asks where the line goes, then where to save; nothing is saved until the
// scope is confirmed.
func (d *dashboard) choosePlacement(ctx context.Context) {
	labels := make([]string, len(placements))
	for i, p := range placements {
		labels[i] = p.label
	}
	pick, ok := d.frame.Choose("Quota line placement", labels, ctx.Done())
	if !ok {
		return
	}
	value := ""
	for _, p := range placements {
		if p.label == pick {
			value = p.value
		}
	}
	slabels := make([]string, len(scopes))
	for i, s := range scopes {
		slabels[i] = s.label
	}
	spick, ok := d.frame.Choose("Save placement to", slabels, ctx.Done())
	if !ok {
		return
	}
	for _, s := range scopes {
		if s.label != spick {
			continue
		}
		res, err := d.st.settings.Save(ctx, s.scope, []config.Edit{{Key: "quotas.linePlacement", Value: value}})
		if err != nil {
			d.notice("Placement not saved: "+err.Error()+" (try again or pick another scope)", true)
			return
		}
		msg := fmt.Sprintf("Line placement %s saved to %s.", value, s.scope)
		if len(res.Shadowed) > 0 {
			msg += " A higher-priority scope still overrides: " + strings.Join(res.Shadowed, ", ") + "."
		}
		d.notice(msg, len(res.Shadowed) > 0)
	}
}

// modalDialogs adapts the frame's choice dialog to the reset flow.
type modalDialogs struct {
	ctx   context.Context
	frame *frame.Frame
}

func (m modalDialogs) Interactive() bool { return true }

func (m modalDialogs) Select(title string, options []string) (string, bool, error) {
	choice, ok := m.frame.Choose(title, options, m.ctx.Done())
	return choice, ok, nil
}

func (d *dashboard) reset(ctx context.Context, id string) {
	res, err := resetflow.Run(ctx, d.st.quotas, modalDialogs{ctx: ctx, frame: d.frame}, id, quota.FromPanel)
	if err != nil {
		if ctx.Err() == nil {
			d.notice("Reset failed: "+err.Error(), true)
		}
		return
	}
	d.notice(res.Text, res.Warn)
}

func (d *dashboard) providerRows() []uiproviders.Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rows
}

// rereadProviders refreshes credential state off the render path.
func (d *dashboard) rereadProviders(host sdk.Context) {
	d.async(func(context.Context) {
		rows := d.a.providerRows(host, time.Now())
		d.mu.Lock()
		d.rows = rows
		d.mu.Unlock()
		d.frame.Invalidate()
	})
}

// previewTemplate renders src as the line would show it now, without touching the live line.
func (d *dashboard) previewTemplate(src string, width int) ([]string, []string) {
	reg := d.st.quotas.Registry()
	tpl := line.Parse(src, func(n string) string {
		if s := reg.Resolve(n); s != nil {
			return s.ID
		}
		return ""
	})
	s := d.a.settings()
	th := uitheme.Theme{T: d.st.host.UITheme()}
	short := s.AlwaysShortIDs || width < s.CompactWidthCols
	segs := line.Render(th, tpl, d.st.quotas.View(), lineConfig(s), time.Now().UnixMilli(), short, nil)
	return line.Pack(th, segs, max(width, 10)), tpl.Unknown
}

// saveSettings asks for the scope, then writes the staged edits; nothing is written until the
// scope is confirmed, and a failed save keeps the drafts.
func (d *dashboard) saveSettings(ctx context.Context, edits []config.Edit) {
	labels := make([]string, len(scopes))
	for i, s := range scopes {
		labels[i] = s.label
	}
	pick, ok := d.frame.Choose(fmt.Sprintf("Save %d setting(s) to", len(edits)), labels, ctx.Done())
	if !ok {
		return
	}
	for _, s := range scopes {
		if s.label != pick {
			continue
		}
		res, err := d.st.settings.Save(ctx, s.scope, edits)
		if err != nil {
			d.sett.SetNotice("Not saved: "+err.Error()+" (the drafts are kept)", true)
			d.frame.Invalidate()
			return
		}
		keys := make([]string, len(edits))
		for i, e := range edits {
			keys[i] = e.Key
		}
		d.sett.Saved(keys)
		msg := fmt.Sprintf("Saved %d setting(s) to %s.", len(edits), s.scope)
		if len(res.Shadowed) > 0 {
			msg += " A higher-priority scope still overrides: " + strings.Join(res.Shadowed, ", ") + "."
		}
		d.sett.SetNotice(msg, len(res.Shadowed) > 0)
		d.frame.Invalidate()
	}
}

// knownProviders lists the ids a provider-list setting offers.
func (d *dashboard) knownProviders(key string) []string {
	switch key {
	case "quotas.providers":
		var ids []string
		for _, s := range d.st.quotas.Sources() {
			ids = append(ids, s.ID)
		}
		return ids
	case "openai.verbosityProviders":
		return []string{"openai", "openai-codex"}
	case "claude.cacheRetentionProviders":
		return []string{"claude", "anthropic"}
	}
	return nil
}

func (d *dashboard) catalogRows() []uimodels.Model {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.catalog
}

// rereadCatalog loads the model list off the render path.
func (d *dashboard) rereadCatalog(host sdk.Context) {
	d.async(func(context.Context) {
		rows, err := d.a.catalogRows(host)
		d.mu.Lock()
		d.catalog, d.catalogErr = rows, ""
		if err != nil {
			d.catalogErr = err.Error()
		}
		d.mu.Unlock()
		d.frame.Invalidate()
	})
}

func (d *dashboard) catalogError() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.catalogErr
}

// saveOverride writes a model's staged override fields in one locked update, then asks the host
// to reread its catalog without network access. A failed write keeps the drafts.
func (d *dashboard) saveOverride(ctx context.Context, host sdk.Context, m uimodels.Model, ops []modelsfile.Op) {
	if err := modelsfile.Update(ctx, d.st.paths.Models(), m.Provider, m.ID, ops); err != nil {
		d.models.SetNotice("Override not saved: "+err.Error()+" (the staged fields are kept)", true)
		d.frame.Invalidate()
		return
	}
	d.models.OverrideSaved()
	no := false
	msg := "Saved " + fmt.Sprint(len(ops)) + " field(s) to models.json."
	warn := false
	if res, err := host.ModelRegistry().Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &no}); err != nil {
		msg, warn = msg+" PiG could not reload the catalog: "+err.Error(), true
	} else if len(res.Errors) > 0 {
		msg, warn = msg+" The catalog reload reported errors for some providers.", true
	} else {
		msg += " The catalog was reloaded; a selected model picks the change up after you reselect it."
	}
	d.rereadCatalog(host)
	d.models.SetNotice(msg, warn)
	d.frame.Invalidate()
}

func (d *dashboard) aliasDefinition(m uimodels.Model) (uimodels.Alias, bool) {
	for _, a := range d.a.settings().Aliases {
		if a.Provider == m.Provider && a.ID == m.ID {
			return uimodels.Alias{Provider: a.Provider, Source: a.Source, ID: a.ID, Name: a.Name, ContextWindow: a.ContextWindow, MaxTokens: a.MaxTokens}, true
		}
	}
	return uimodels.Alias{}, false
}

// chooseScope asks where a models.aliases change is saved.
func (d *dashboard) chooseScope(ctx context.Context, title string) (config.Scope, bool) {
	labels := make([]string, len(scopes))
	for i, s := range scopes {
		labels[i] = s.label
	}
	pick, ok := d.frame.Choose(title, labels, ctx.Done())
	if !ok {
		return 0, false
	}
	for _, s := range scopes {
		if s.label == pick {
			return s.scope, true
		}
	}
	return 0, false
}

// saveAliases writes the whole alias list; a higher scope's list replaces a lower one, so the
// list written is the effective one with the change applied.
func (d *dashboard) saveAliases(ctx context.Context, scope config.Scope, list []config.Alias, done string) bool {
	res, err := d.st.settings.Save(ctx, scope, []config.Edit{{Key: "models.aliases", Value: list}})
	if err != nil {
		d.models.SetNotice("Alias not saved: "+err.Error()+" (the form is kept)", true)
		d.frame.Invalidate()
		return false
	}
	msg := fmt.Sprintf("%s (%s).", done, scope)
	if len(res.Shadowed) > 0 {
		msg += " A higher-priority scope still overrides models.aliases."
	}
	d.models.SetNotice(msg, len(res.Shadowed) > 0)
	d.rereadCatalog(d.st.host)
	d.frame.Invalidate()
	return true
}

func (d *dashboard) saveAlias(ctx context.Context, old *uimodels.Model, a uimodels.Alias) {
	scope, ok := d.chooseScope(ctx, "Save alias "+a.Provider+"/"+a.ID+" to")
	if !ok {
		return
	}
	next := config.Alias{Provider: a.Provider, Source: a.Source, ID: a.ID, Name: a.Name, ContextWindow: a.ContextWindow, MaxTokens: a.MaxTokens}
	var list []config.Alias
	replaced := false
	for _, e := range d.a.settings().Aliases {
		if old != nil && e.Provider == old.Provider && e.ID == old.ID {
			list, replaced = append(list, next), true
			continue
		}
		list = append(list, e)
	}
	if !replaced {
		list = append(list, next)
	}
	if d.saveAliases(ctx, scope, list, "Alias "+a.Provider+"/"+a.ID+" saved") {
		d.models.AliasSaved()
	}
}

func (d *dashboard) removeAlias(ctx context.Context, m uimodels.Model) {
	pick, ok := d.frame.Choose("Remove alias "+m.Key()+"?", []string{"Keep it", "Remove it"}, ctx.Done())
	if !ok || pick != "Remove it" {
		return
	}
	scope, ok := d.chooseScope(ctx, "Remove from")
	if !ok {
		return
	}
	var list []config.Alias
	for _, e := range d.a.settings().Aliases {
		if e.Provider != m.Provider || e.ID != m.ID {
			list = append(list, e)
		}
	}
	d.saveAliases(ctx, scope, list, "Alias "+m.Key()+" removed")
}
