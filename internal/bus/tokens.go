package bus

import (
	"context"
	"fmt"
	"sync"

	"github.com/furbyhaxx/pig-auth-plus/internal/tokens"
)

const opTokenQuery = "query"

// TokenTracker is the token tracker as the bus uses it.
type TokenTracker interface {
	Latest() *tokens.Report
	OnChange(fn func(*tokens.Report, tokens.Reason)) func()
	Refresh(ctx context.Context, reason tokens.Reason) (tokens.Report, bool)
	Breakdown(ctx context.Context) ([]tokens.Part, bool)
}

// TokensOptions configure the token service.
type TokensOptions struct {
	Tracker   TokenTracker
	Transport Transport
	Go        Runner
	Warn      func(string)
}

// Tokens serves the token channels for one session.
type Tokens struct {
	o   TokensOptions
	out *outbox

	mu   sync.Mutex
	offs []func()
}

// StartTokens registers the query channel, forwards tracker changes as broadcasts and
// announces the service; it ends with the runner's session.
func StartTokens(o TokensOptions) (*Tokens, error) {
	t := &Tokens{o: o, out: newOutbox(o.Transport, o.Warn)}
	if !o.Go(t.out.run) {
		return nil, context.Canceled
	}
	channel := TokenPrefix + opTokenQuery
	off, err := o.Transport.On(channel, func(data any) {
		req, ok := decode(data)
		if !ok {
			return
		}
		o.Go(func(c context.Context) { t.query(c, replier{t: o.Transport, ctx: c, warn: o.Warn}, req) })
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", channel, err)
	}
	t.mu.Lock()
	t.offs = append(t.offs, off, o.Tracker.OnChange(t.changed))
	t.mu.Unlock()
	t.out.post(TokenPrefix+"ready", map[string]any{"v": Version, "capabilities": []string{"query", "context"}})
	o.Go(func(c context.Context) { <-c.Done(); t.Close() })
	return t, nil
}

// Close stops listening; it is safe to call more than once.
func (t *Tokens) Close() {
	t.mu.Lock()
	offs := t.offs
	t.offs = nil
	t.mu.Unlock()
	for _, off := range offs {
		off()
	}
}

func (t *Tokens) changed(r *tokens.Report, reason tokens.Reason) {
	t.out.post(TokenPrefix+"context", map[string]any{"v": Version, "reason": reason, "report": WireReport(r)})
}

func (t *Tokens) query(ctx context.Context, rep replier, req request) {
	ch := TokenPrefix + opTokenQuery + ":reply"
	refresh, ok := optionalBool(req.payload, "refresh")
	if !ok {
		rep.fail(ch, req.nonce, "refresh must be a boolean")
		return
	}
	breakdown, ok := optionalBool(req.payload, "breakdown")
	if !ok {
		rep.fail(ch, req.nonce, "breakdown must be a boolean")
		return
	}
	var report *tokens.Report
	if refresh {
		if r, counted := t.o.Tracker.Refresh(ctx, tokens.ReasonRequest); counted {
			report = &r
		}
	}
	if report == nil {
		report = t.o.Tracker.Latest()
	}
	out := map[string]any{"v": Version, "ok": true, "report": WireReport(report)}
	if breakdown {
		if parts, counted := t.o.Tracker.Breakdown(ctx); counted {
			wire := make([]map[string]any, 0, len(parts))
			for _, p := range parts {
				wire = append(wire, map[string]any{"kind": p.Kind, "count": wireCount(p.Count), "items": p.Items})
			}
			out["breakdown"] = wire
		}
	}
	rep.send(ch, req.nonce, out)
}

// WireReport is the v1 ContextReport of r; nil becomes JSON null. Unknown optional numbers
// are omitted, never zero.
func WireReport(r *tokens.Report) any {
	if r == nil {
		return nil
	}
	w := map[string]any{
		"provider":     r.Provider,
		"model":        r.Model,
		"tokens":       wireCount(r.Tokens),
		"tail":         wireCount(r.Tail),
		"tailMessages": r.TailMessages,
		"computedAt":   r.ComputedAt.UnixMilli(),
	}
	if r.ContextWindow > 0 {
		w["contextWindow"] = r.ContextWindow
	}
	if r.Percent != nil {
		w["percent"] = *r.Percent
	}
	if r.Anchor != nil {
		w["anchor"] = wireCount(*r.Anchor)
	}
	if r.HostTokens != nil {
		w["pi"] = *r.HostTokens
	}
	return w
}

func wireCount(c tokens.Count) map[string]any {
	w := map[string]any{"tokens": c.Tokens, "source": c.Source}
	if len(c.Diagnostics) > 0 {
		diags := make([]map[string]any, 0, len(c.Diagnostics))
		for _, d := range c.Diagnostics {
			diags = append(diags, map[string]any{"code": "needs-auth", "provider": d.Provider, "message": d.Message})
		}
		w["diagnostics"] = diags
	}
	return w
}
