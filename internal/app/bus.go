package app

import (
	"context"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/bus"
	"github.com/furbyhaxx/pig-auth-plus/internal/resetflow"
)

// hostBus adapts PiG's shared event bus to the bus package's transport.
type hostBus struct{ ctx sdk.Context }

func (h hostBus) On(channel string, fn func(any)) (func(), error) {
	return h.ctx.Events().On(channel, func(_ sdk.Context, data any) error {
		fn(data)
		return nil
	})
}

func (h hostBus) Emit(channel string, payload any) error {
	return h.ctx.Events().Emit(channel, payload)
}

// startBus serves the pi-auth-plus v1 quota and token channels for this session.
func (a *App) startBus(st *state) {
	warn := func(msg string) { st.host.Notify(msg, "warning") }
	transport := hostBus{st.host}
	q, err := bus.StartQuotas(bus.QuotasOptions{
		Engine: st.quotas, Transport: transport, Go: st.group.Go, Warn: warn,
		Interval: func() time.Duration { return time.Duration(a.settings().RefreshIntervalSec) * time.Second },
		Approve:  func(ctx context.Context, ap bus.Approval) (bool, error) { return a.approveBusReset(ctx, st, ap) },
	})
	if err != nil {
		warn("pig-auth-plus bus (quotas): " + err.Error())
	} else {
		st.busClosers = append(st.busClosers, q.Close)
	}
	t, err := bus.StartTokens(bus.TokensOptions{Tracker: st.tokens, Transport: transport, Go: st.group.Go, Warn: warn})
	if err != nil {
		warn("pig-auth-plus bus (tokens): " + err.Error())
		return
	}
	st.busClosers = append(st.busClosers, t.Close)
}

// approveBusReset asks the user in this session whether another extension may spend a credit;
// without a TUI nothing can be approved.
func (a *App) approveBusReset(_ context.Context, st *state, ap bus.Approval) (bool, error) {
	dialogs := uiDialogs{st.host}
	if !dialogs.Interactive() {
		return false, nil
	}
	title := "Another extension asks to spend a reset credit.\n" + resetflow.List(st.quotas, ap.List) +
		"\nCredit to use: " + resetflow.Describe(ap.Credit)
	choice, answered, err := dialogs.Select(title, []string{resetflow.Keep, resetflow.Spend})
	if err != nil || !answered {
		return false, err
	}
	return choice == resetflow.Spend, nil
}
