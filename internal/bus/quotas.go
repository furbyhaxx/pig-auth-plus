package bus

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/resetflow"
)

// Quota channel names (without the prefix).
const (
	opQuery   = "query"
	opRefresh = "refresh"
	opResets  = "resets"
	opReset   = "reset"
)

// QuotaEngine is the quota engine as the bus uses it.
type QuotaEngine interface {
	Sources() []*quota.Source
	Registry() *quota.Registry
	View() []quota.View
	Refresh(ctx context.Context, id string, o quota.RefreshOptions) error
	ListResets(ctx context.Context, id string) (quota.ResetList, error)
	ConsumeReset(ctx context.Context, id, creditID string, by quota.Requester) (quota.ResetOutcome, error)
	OnChange(fn func()) func()
	OnResetConsumed(fn func(quota.ResetOutcome, quota.Requester)) func()
}

// Approval is what the user is asked to approve for a bus-requested reset.
type Approval struct {
	Provider string
	Label    string
	List     quota.ResetList
	Credit   quota.ResetCredit
}

// Approver asks the user, in the owning session, whether to spend one credit. It must return
// false when no dialog can be shown or the user dismissed it.
type Approver func(ctx context.Context, a Approval) (bool, error)

// QuotasOptions configure the quota service.
type QuotasOptions struct {
	Engine    QuotaEngine
	Transport Transport
	Go        Runner
	// Interval is the configured refresh interval, used to decide staleness.
	Interval func() time.Duration
	Approve  Approver
	Now      func() time.Time
	Warn     func(string)
}

// Quotas serves the quota channels for one session.
type Quotas struct {
	o      QuotasOptions
	out    *outbox
	mu     sync.Mutex
	rows   map[string]ProviderRow
	states map[string]string
	resets sync.Mutex // one reset at a time: an approval dialog is modal and a consume is never retried
	offs   []func()
}

// StartQuotas registers the channels and announces the service; the service ends with the
// runner's session.
func StartQuotas(o QuotasOptions) (*Quotas, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	q := &Quotas{o: o, out: newOutbox(o.Transport, o.Warn), rows: map[string]ProviderRow{}, states: map[string]string{}}
	if !o.Go(q.out.run) {
		return nil, context.Canceled
	}
	handlers := map[string]func(context.Context, replier, request){
		opQuery: q.query, opRefresh: q.refresh, opResets: q.listResets, opReset: q.reset,
	}
	for op, h := range handlers {
		channel := QuotaPrefix + op
		off, err := o.Transport.On(channel, func(data any) {
			req, ok := decode(data)
			if !ok {
				return
			}
			o.Go(func(c context.Context) { h(c, replier{t: o.Transport, ctx: c, warn: o.Warn}, req) })
		})
		if err != nil {
			q.Close()
			return nil, fmt.Errorf("subscribe %s: %w", channel, err)
		}
		q.offs = append(q.offs, off)
	}
	q.offs = append(q.offs, o.Engine.OnChange(q.changed), o.Engine.OnResetConsumed(q.consumed))
	q.seed()
	q.out.post(QuotaPrefix+"ready", q.ready())
	o.Go(func(c context.Context) { <-c.Done(); q.Close() })
	return q, nil
}

// Close stops listening and broadcasting; it is safe to call more than once.
func (q *Quotas) Close() {
	q.mu.Lock()
	offs := q.offs
	q.offs = nil
	q.mu.Unlock()
	for _, off := range offs {
		off()
	}
}

func (q *Quotas) interval() time.Duration {
	if q.o.Interval == nil {
		return time.Minute
	}
	return q.o.Interval()
}

func (q *Quotas) ready() map[string]any {
	type provider struct {
		ID      string   `json:"id"`
		Aliases []string `json:"aliases"`
	}
	var providers []provider
	for _, s := range q.o.Engine.Sources() {
		aliases := s.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		providers = append(providers, provider{ID: s.ID, Aliases: aliases})
	}
	if providers == nil {
		providers = []provider{}
	}
	return map[string]any{
		"v": Version, "providers": providers,
		"capabilities": []string{"query", "refresh", "resets", "reset", "snapshot", "event"},
	}
}

// seed records the current rows so the first change broadcasts only real differences.
func (q *Quotas) seed() {
	now := q.o.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, v := range q.o.Engine.View() {
		row := BuildProvider(v, now, q.interval())
		q.rows[row.ID] = fingerprintRow(row)
		q.states[row.ID] = row.State
	}
}

// changed broadcasts the providers whose report row changed and the auth/rate-limit transitions.
func (q *Quotas) changed() {
	now := q.o.Now()
	var snapshots []ProviderRow
	var events []map[string]any
	q.mu.Lock()
	for _, v := range q.o.Engine.View() {
		row := BuildProvider(v, now, q.interval())
		stable := fingerprintRow(row)
		if prev, ok := q.rows[row.ID]; !ok || !sameRow(prev, stable) {
			q.rows[row.ID] = stable
			snapshots = append(snapshots, row)
		}
		if q.states[row.ID] == row.State {
			continue
		}
		q.states[row.ID] = row.State
		switch row.State {
		case StateNeedsAuth:
			ev := map[string]any{"v": Version, "kind": "auth-required", "provider": row.ID}
			if row.Error != "" {
				ev["message"] = resetflow.Plain(row.Error)
			}
			events = append(events, ev)
		case StateRateLimited:
			events = append(events, map[string]any{"v": Version, "kind": "rate-limited", "provider": row.ID, "status": v.Backoff.Status})
		}
	}
	q.mu.Unlock()
	for _, row := range snapshots {
		q.out.post(QuotaPrefix+"snapshot", map[string]any{"v": Version, "provider": row})
	}
	for _, ev := range events {
		q.out.post(QuotaPrefix+"event", ev)
	}
}

func sameRow(a, b ProviderRow) bool {
	if a.ID != b.ID || a.Title != b.Title || a.Short != b.Short || a.State != b.State || a.Tier != b.Tier ||
		a.NeedsAuth != b.NeedsAuth || a.FetchedAt != b.FetchedAt || a.Source != b.Source || a.Error != b.Error {
		return false
	}
	if (a.Resets == nil) != (b.Resets == nil) || (a.Resets != nil && *a.Resets != *b.Resets) {
		return false
	}
	return reflect.DeepEqual(a.Windows, b.Windows) && reflect.DeepEqual(a.Credits, b.Credits)
}

func (q *Quotas) consumed(o quota.ResetOutcome, by quota.Requester) {
	q.out.post(QuotaPrefix+"event", map[string]any{
		"v": Version, "kind": "reset-consumed", "provider": o.Provider, "code": string(o.Code),
		"windowsReset": o.WindowsReset, "requester": string(by),
	})
}

// selection resolves the providers named by a request to enabled sources, in registry order.
// Omitted or null selects every enabled source; an empty list selects none.
func (q *Quotas) selection(p map[string]any) ([]*quota.Source, error) {
	names, present, ok := optionalStrings(p, "providers")
	if !ok {
		return nil, fmt.Errorf("providers must be an array of strings")
	}
	enabled := q.o.Engine.Sources()
	if !present {
		return enabled, nil
	}
	want := map[string]bool{}
	for _, name := range names {
		s := q.o.Engine.Registry().Resolve(name)
		if s == nil {
			return nil, fmt.Errorf("unknown quota provider %q; known: %s", resetflow.Plain(name), idList(q.o.Engine.Registry().All()))
		}
		if !slices.ContainsFunc(enabled, func(e *quota.Source) bool { return e.ID == s.ID }) {
			return nil, fmt.Errorf("quota provider %q is disabled; enable it in quotas.providers", s.ID)
		}
		want[s.ID] = true
	}
	var out []*quota.Source
	for _, s := range enabled {
		if want[s.ID] {
			out = append(out, s)
		}
	}
	return out, nil
}

func idList(srcs []*quota.Source) string {
	ids := make([]string, len(srcs))
	for i, s := range srcs {
		ids[i] = s.ID
	}
	return strings.Join(ids, ", ")
}

// report builds the QuotaReport for the selected sources from the engine's current views.
func (q *Quotas) report(sel []*quota.Source) Report {
	now := q.o.Now()
	byID := map[string]quota.View{}
	for _, v := range q.o.Engine.View() {
		byID[v.Source.ID] = v
	}
	rep := Report{V: Version, GeneratedAt: now.UnixMilli(), Providers: []ProviderRow{}}
	for _, s := range sel {
		if v, ok := byID[s.ID]; ok {
			rep.Providers = append(rep.Providers, BuildProvider(v, now, q.interval()))
		}
	}
	return rep
}

// refreshAll refreshes the selected sources concurrently and waits for them.
func (q *Quotas) refreshAll(ctx context.Context, sel []*quota.Source, o quota.RefreshOptions) {
	var wg sync.WaitGroup
	for _, s := range sel {
		wg.Go(func() { _ = q.o.Engine.Refresh(ctx, s.ID, o) })
	}
	wg.Wait()
}

func (q *Quotas) query(ctx context.Context, rep replier, req request) {
	ch := QuotaPrefix + opQuery + ":reply"
	sel, err := q.selection(req.payload)
	if err != nil {
		rep.fail(ch, req.nonce, err.Error())
		return
	}
	age, present, ok := optionalNumber(req.payload, "maxAgeSec")
	if !ok {
		rep.fail(ch, req.nonce, "maxAgeSec must be a finite non-negative number")
		return
	}
	if present {
		q.refreshAll(ctx, sel, quota.RefreshOptions{MinAge: max(time.Duration(age*float64(time.Second)), time.Second)})
	}
	rep.send(ch, req.nonce, map[string]any{"v": Version, "ok": true, "report": q.report(sel)})
}

func (q *Quotas) refresh(ctx context.Context, rep replier, req request) {
	ch := QuotaPrefix + opRefresh + ":reply"
	sel, err := q.selection(req.payload)
	if err != nil {
		rep.fail(ch, req.nonce, err.Error())
		return
	}
	force, ok := optionalBool(req.payload, "force")
	if !ok {
		rep.fail(ch, req.nonce, "force must be a boolean")
		return
	}
	q.refreshAll(ctx, sel, quota.RefreshOptions{Force: force})
	rep.send(ch, req.nonce, map[string]any{"v": Version, "ok": true, "report": q.report(sel)})
}

// resetTarget resolves the provider of a reset request to its id.
func (q *Quotas) resetTarget(p map[string]any) (string, error) {
	name, ok := optionalString(p, "provider")
	if !ok || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("provider is required")
	}
	s := q.o.Engine.Registry().Resolve(name)
	if s == nil {
		return "", fmt.Errorf("unknown quota provider %q; known: %s", resetflow.Plain(name), idList(q.o.Engine.Registry().All()))
	}
	return s.ID, nil
}

func (q *Quotas) listResets(ctx context.Context, rep replier, req request) {
	ch := QuotaPrefix + opResets + ":reply"
	id, err := q.resetTarget(req.payload)
	if err != nil {
		rep.fail(ch, req.nonce, err.Error())
		return
	}
	list, err := q.o.Engine.ListResets(ctx, id)
	if err != nil {
		rep.fail(ch, req.nonce, resetflow.Plain(err.Error()))
		return
	}
	rep.send(ch, req.nonce, map[string]any{"v": Version, "ok": true, "resets": list})
}

func (q *Quotas) reset(ctx context.Context, rep replier, req request) {
	ch := QuotaPrefix + opReset + ":reply"
	id, err := q.resetTarget(req.payload)
	if err != nil {
		rep.fail(ch, req.nonce, err.Error())
		return
	}
	if confirm, _ := req.payload["confirm"].(bool); !confirm {
		rep.fail(ch, req.nonce, "a reset needs confirm: true and the user's approval; nothing was spent")
		return
	}
	creditID, ok := optionalString(req.payload, "creditId")
	if !ok {
		rep.fail(ch, req.nonce, "creditId must be a string")
		return
	}
	q.resets.Lock()
	defer q.resets.Unlock()
	list, err := q.o.Engine.ListResets(ctx, id)
	if err != nil {
		rep.fail(ch, req.nonce, resetflow.Plain(err.Error()))
		return
	}
	credit, ok := resetflow.Pick(list, creditID)
	if !ok {
		if creditID != "" {
			rep.fail(ch, req.nonce, "that credit is not available; nothing was spent")
		} else {
			rep.fail(ch, req.nonce, "no redeemable credit; nothing was spent")
		}
		return
	}
	if q.o.Approve == nil {
		rep.fail(ch, req.nonce, "no user interface can approve a reset; nothing was spent")
		return
	}
	label := id
	if s := q.o.Engine.Registry().ByID(id); s != nil {
		label = s.Label
	}
	approved, err := q.o.Approve(ctx, Approval{Provider: id, Label: label, List: list, Credit: credit})
	if err != nil {
		rep.fail(ch, req.nonce, "approval failed: "+resetflow.Plain(err.Error())+"; nothing was spent")
		return
	}
	if !approved {
		rep.fail(ch, req.nonce, "the user kept the credit; nothing was spent")
		return
	}
	outcome, err := q.o.Engine.ConsumeReset(ctx, id, credit.ID, quota.FromBus)
	if err != nil {
		rep.fail(ch, req.nonce, resetflow.Plain(err.Error())+"; the credit may already be spent: list resets again before any further attempt")
		return
	}
	rep.send(ch, req.nonce, map[string]any{"v": Version, "ok": true, "outcome": outcome})
}
