package bus

import (
	"math"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Provider states of a report row.
const (
	StateOK          = "ok"
	StateStale       = "stale"
	StateError       = "error"
	StateNeedsAuth   = "needs-auth"
	StateRateLimited = "rate-limited"
	StateUnknown     = "unknown"
)

// Report is the QuotaReport shared by query and refresh replies.
type Report struct {
	V           int           `json:"v"`
	GeneratedAt int64         `json:"generatedAt"`
	Providers   []ProviderRow `json:"providers"`
}

// ProviderRow is one provider's current report row. Unknown numbers are omitted, never zero.
type ProviderRow struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Short     string       `json:"short"`
	State     string       `json:"state"`
	Windows   []WindowRow  `json:"windows"`
	Resets    *ResetsRow   `json:"resets,omitempty"`
	Credits   *CreditsRow  `json:"credits,omitempty"`
	Tier      string       `json:"tier,omitempty"`
	NeedsAuth bool         `json:"needsAuth,omitempty"`
	FetchedAt int64        `json:"fetchedAt,omitempty"`
	AgeSec    *int64       `json:"ageSec,omitempty"`
	Source    quota.Origin `json:"source,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// ResetsRow carries the provider-reported count of on-demand reset credits.
type ResetsRow struct {
	Available int `json:"available"`
}

// WindowRow is one usage window.
type WindowRow struct {
	ID               string       `json:"id"`
	ShortLabel       string       `json:"shortLabel"`
	Label            string       `json:"label"`
	UsedFraction     *float64     `json:"usedFraction,omitempty"`
	ValueText        string       `json:"valueText,omitempty"`
	ResetAtMs        *int64       `json:"resetAtMs,omitempty"`
	Important        bool         `json:"important,omitempty"`
	Group            string       `json:"group,omitempty"`
	Period           string       `json:"period,omitempty"`
	Scope            string       `json:"scope,omitempty"`
	UsedPercent      *float64     `json:"usedPercent,omitempty"`
	RemainingPercent *float64     `json:"remainingPercent,omitempty"`
	ResetAt          *int64       `json:"resetAt,omitempty"`
	ResetInSec       *int64       `json:"resetInSec,omitempty"`
	Drainage         *DrainageRow `json:"drainage,omitempty"`
}

// CreditsRow is a prepaid or capped balance.
type CreditsRow struct {
	Remaining  *float64     `json:"remaining,omitempty"`
	Total      *float64     `json:"total,omitempty"`
	Used       *float64     `json:"used,omitempty"`
	Currency   string       `json:"currency"`
	DetailText string       `json:"detailText,omitempty"`
	Drainage   *DrainageRow `json:"drainage,omitempty"`
}

// DrainageRow is the observed consumption rate; window rates are fractions per hour, credit
// rates are currency units per hour.
type DrainageRow struct {
	ObservedAt             int64    `json:"observedAt"`
	Window                 *RateRow `json:"window,omitempty"`
	H1                     *RateRow `json:"h1,omitempty"`
	H24                    *RateRow `json:"h24,omitempty"`
	ETASec                 *float64 `json:"etaSec,omitempty"`
	ResetsBeforeExhaustion bool     `json:"resetsBeforeExhaustion,omitempty"`
}

// RateRow is a rate and the sample coverage it was computed over.
type RateRow struct {
	PerHour     float64 `json:"perHour"`
	CoverageSec float64 `json:"coverageSec"`
}

// staleFactor is how many refresh intervals old data may be before it is stale.
const staleFactor = 2

// BuildProvider maps one engine view to its report row at now. interval is the configured
// refresh interval; the source's polling floor raises it.
func BuildProvider(v quota.View, now time.Time, interval time.Duration) ProviderRow {
	nowMs := now.UnixMilli()
	row := ProviderRow{ID: v.Source.ID, Title: v.Source.Label, Short: v.Source.Short, Windows: []WindowRow{}}
	snap := v.Snapshot
	fetched := v.FetchedAt
	if snap != nil {
		if fetched == 0 {
			fetched = snap.FetchedAt
		}
		row.Tier = snap.Tier
		row.Source = snap.Source
		if snap.ResetCredits != nil {
			row.Resets = &ResetsRow{Available: *snap.ResetCredits}
		}
		metrics := metricsByKey(v.Drainage, nowMs)
		for _, w := range snap.Windows {
			row.Windows = append(row.Windows, windowRow(w, nowMs, metrics[string(quota.KindWindow)+"\x00"+w.ID]))
		}
		if c := snap.Credits; c != nil {
			row.Credits = &CreditsRow{
				Remaining: c.Remaining, Total: c.Total, Used: c.Used, Currency: c.Currency, DetailText: c.Detail,
				Drainage: drainageRow(metrics[string(quota.KindCredits)+"\x00"+c.Currency]),
			}
		}
	}
	if fetched > 0 {
		row.FetchedAt = fetched
		age := max((nowMs-fetched)/1000, 0)
		row.AgeSec = &age
	}
	backoffActive := v.Backoff != nil && v.Backoff.Until > nowMs
	rateLimited := backoffActive && (v.Backoff.Status == 403 || v.Backoff.Status == 429)
	switch {
	case snap != nil && snap.NeedsAuth:
		row.State, row.NeedsAuth = StateNeedsAuth, true
		row.Error = snap.Error
	case rateLimited:
		row.State = StateRateLimited
		row.Error = v.Backoff.Error
	case (snap != nil && snap.Error != "") || backoffActive:
		row.State = StateError
		if snap != nil && snap.Error != "" {
			row.Error = snap.Error
		} else {
			row.Error = v.Backoff.Error
		}
	case snap == nil:
		row.State = StateUnknown
	case fetched > 0 && now.Sub(time.UnixMilli(fetched)) > staleFactor*max(interval, v.Source.MinPoll):
		row.State = StateStale
	default:
		row.State = StateOK
	}
	return row
}

func metricsByKey(histories []quota.History, nowMs int64) map[string]*quota.Metrics {
	out := make(map[string]*quota.Metrics, len(histories))
	for i := range histories {
		h := histories[i]
		out[string(h.Kind)+"\x00"+h.ID] = quota.Compute(&h, nowMs)
	}
	return out
}

func windowRow(w quota.Window, nowMs int64, m *quota.Metrics) WindowRow {
	row := WindowRow{
		ID: w.ID, ShortLabel: w.Short, Label: w.Label, UsedFraction: w.Used, ValueText: w.Value, ResetAtMs: w.ResetAtMs,
		Important: w.Important, Group: w.Group, Period: w.Period, Scope: w.Scope, ResetAt: w.ResetAtMs,
		Drainage: drainageRow(m),
	}
	if w.Used != nil {
		used, remaining := percent(*w.Used), percent(1-*w.Used)
		row.UsedPercent, row.RemainingPercent = &used, &remaining
	}
	if w.ResetAtMs != nil {
		in := max((*w.ResetAtMs-nowMs)/1000, 0)
		row.ResetInSec = &in
	}
	return row
}

// percent converts a fraction to a percentage without floating-point noise such as 28.999999.
func percent(f float64) float64 { return math.Round(f*1e6) / 1e4 }

func drainageRow(m *quota.Metrics) *DrainageRow {
	if m == nil {
		return nil
	}
	rate := func(r *quota.Rate) *RateRow {
		if r == nil {
			return nil
		}
		return &RateRow{PerHour: r.PerHour, CoverageSec: r.CoverageSec}
	}
	return &DrainageRow{
		ObservedAt: m.ObservedAt, Window: rate(m.Window), H1: rate(m.H1), H24: rate(m.H24),
		ETASec: m.ETASec, ResetsBeforeExhaustion: m.ResetsBeforeExhaustion,
	}
}

// fingerprintRow is the row without fields that drift with the clock, so an unchanged
// provider is not rebroadcast every second.
func fingerprintRow(r ProviderRow) ProviderRow {
	r.AgeSec = nil
	windows := make([]WindowRow, len(r.Windows))
	for i, w := range r.Windows {
		w.ResetInSec = nil
		windows[i] = w
	}
	r.Windows = windows
	return r
}
