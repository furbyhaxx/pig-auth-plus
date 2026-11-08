package app

import (
	"context"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/uitheme"
)

const (
	lineKey = "pig-auth-plus:quotas"
	// lineMaxRows leaves the rest of the screen to the conversation.
	lineMaxRows = 8
	// lineTick keeps reset countdowns current between engine changes.
	lineTick = time.Minute
)

// Placement values of quotas.linePlacement.
const (
	placeAbove     = "above-editor"
	placeBelow     = "below-editor-first"
	placeBelowLast = "below-editor-last"
	placeOff       = "off"
)

// lineView owns the quota line surface of one session: which surface is installed, and the
// compiled template. Rendering reads cached state only.
type lineView struct {
	a  *App
	st *state

	mu       sync.Mutex
	surface  string // placement currently installed
	source   string
	compiled *line.Template
}

func (a *App) startLine(st *state) {
	if st.host.Mode() != "tui" || !st.host.HasUI() {
		return
	}
	lv := &lineView{a: a, st: st}
	st.line = lv
	stop := st.quotas.OnChange(lv.refresh)
	st.settings.Subscribe(func(*config.Snapshot) { lv.refresh() })
	off, _ := st.host.OnThemeChange(func(sdk.Context, sdk.UITheme) { lv.refresh() })
	st.group.Go(func(ctx context.Context) {
		t := time.NewTicker(lineTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				stop()
				if off != nil {
					off()
				}
				return
			case <-t.C:
				lv.refresh()
			}
		}
	})
	lv.refresh()
}

// refreshLine re-applies the placement after a settings or model change.
func (a *App) refreshLine() {
	if st := a.session(); st != nil && st.line != nil {
		st.line.refresh()
	}
}

// refresh installs the surface for the current placement and asks it to render.
func (lv *lineView) refresh() {
	place := lv.a.settings().LinePlacement
	lv.mu.Lock()
	prev := lv.surface
	lv.surface = place
	lv.mu.Unlock()
	h := lv.st.host
	if prev != "" && prev != place {
		lv.clear(prev)
	}
	switch place {
	case placeAbove:
		_ = h.SetWidgetRenderer(lineKey, lv.rows, sdk.WidgetOptions{"placement": "aboveEditor"})
	case placeBelow:
		_ = h.SetWidgetRenderer(lineKey, lv.rows, sdk.WidgetOptions{"placement": "belowEditor"})
	case placeBelowLast:
		h.SetStatus(lineKey, lv.status(h.Width()))
	}
}

func (lv *lineView) placement() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.surface
}

func (lv *lineView) clear(place string) {
	switch place {
	case placeAbove, placeBelow:
		_ = lv.st.host.SetWidget(lineKey, nil)
	case placeBelowLast:
		lv.st.host.ClearStatus(lineKey)
	}
}

func (lv *lineView) template(src string) *line.Template {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.compiled == nil || lv.source != src {
		reg := lv.st.quotas.Registry()
		lv.compiled = line.Parse(src, func(n string) string {
			if s := reg.Resolve(n); s != nil {
				return s.ID
			}
			return ""
		})
		lv.source = src
	}
	return lv.compiled
}

func lineConfig(s config.Settings) line.Config {
	return line.Config{
		PercentUsed:       s.PercentDisplay == "used",
		CreditHealthyUSD:  float64(s.CreditHealthyUSD),
		CreditCriticalUSD: float64(s.CreditCriticalUSD),
		HideSignedOut:     s.HideSignedOut,
		CompactWidthCols:  s.CompactWidthCols,
		AlwaysShortIDs:    s.AlwaysShortIDs,
	}
}

// segments renders the template for a width; it does no I/O.
func (lv *lineView) segments(width int) ([]string, line.Theme) {
	s := lv.a.settings()
	th := uitheme.Theme{T: lv.st.host.UITheme()}
	var inUse []string
	if p := lv.st.host.ModelProvider(); p != "" {
		if src := lv.st.quotas.Registry().Resolve(p); src != nil {
			inUse = append(inUse, src.ID)
		}
	}
	short := s.AlwaysShortIDs || width < s.CompactWidthCols
	segs := line.Render(th, lv.template(s.LineTemplate), lv.st.quotas.View(), lineConfig(s), time.Now().UnixMilli(), short, inUse)
	return segs, th
}

// rows is the widget renderer: packed rows within the width.
func (lv *lineView) rows(width int) []string {
	segs, th := lv.segments(width)
	return line.LimitRows(th, line.Pack(th, segs, width), lineMaxRows, width)
}

// status is the single footer entry: every segment on one row, cut to the width.
func (lv *lineView) status(width int) string {
	segs, th := lv.segments(width)
	sep := th.Paint(" · ", line.Style{Fg: &line.Color{Token: "dim"}})
	return tui.TruncateToWidth(strings.Join(segs, sep), max(1, width), "…", false)
}
