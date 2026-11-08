package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/resetflow"
	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
	"github.com/furbyhaxx/pig-auth-plus/internal/ui/frame"
	uiproviders "github.com/furbyhaxx/pig-auth-plus/internal/ui/providers"
	uiquotas "github.com/furbyhaxx/pig-auth-plus/internal/ui/quotas"
	uitokens "github.com/furbyhaxx/pig-auth-plus/internal/ui/tokens"
)

const (
	defaultWidth = 80
	defaultRows  = 24
)

// dashboard is one open /auth panel; it lives until the panel closes.
type dashboard struct {
	a      *App
	st     *state
	frame  *frame.Frame
	provs  *uiproviders.Tab
	quotas *uiquotas.Tab
	tokens *uitokens.Tab

	mu       sync.Mutex
	counting context.CancelFunc
	rows     []uiproviders.Row
}

// openDashboard shows the /auth panel on the tab named by section. solo starts the Quotas tab
// on one provider.
func (a *App) openDashboard(ctx sdk.Context, section, solo string) error {
	st := a.session()
	if st == nil {
		ctx.Notify("pig-auth-plus is not active in this session.", "warning")
		return nil
	}
	if section != "providers" && section != "quotas" && section != "tokens" {
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
	d.frame = frame.New(func() frame.Env {
		w, h := ctx.Width(), ctx.Height()
		if w <= 0 {
			w = defaultWidth
		}
		if h <= 0 {
			h = defaultRows
		}
		return frame.Env{Width: w, Rows: h, Theme: ctx.UITheme()}
	}, section, d.provs, d.quotas, d.tokens)
	stop := st.quotas.OnChange(d.frame.Invalidate)
	defer stop()
	stopTokens := st.tokens.OnChange(func(*tokens.Report, tokens.Reason) {
		d.frame.Invalidate()
		d.count(false)
	})
	defer stopTokens()
	d.count(true)
	d.rereadProviders(ctx)
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
