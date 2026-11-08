package app

import (
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/command"
	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	"github.com/furbyhaxx/pig-auth-plus/internal/resetflow"
	uiproviders "github.com/furbyhaxx/pig-auth-plus/internal/ui/providers"
	uiquotas "github.com/furbyhaxx/pig-auth-plus/internal/ui/quotas"
	uisettings "github.com/furbyhaxx/pig-auth-plus/internal/ui/settings"
	uitokens "github.com/furbyhaxx/pig-auth-plus/internal/ui/tokens"
)

const headlessWidth = 100

func (a *App) registerCommands(ext *sdk.Extension) {
	ext.RegisterCommand("auth", sdk.CommandOptions{
		Description:            "Provider status, quotas, token counts and settings",
		GetArgumentCompletions: a.authCompletions,
		Handler:                a.authCommand,
	})
	for _, name := range []string{"tokens", "token"} {
		ext.RegisterCommand(name, sdk.CommandOptions{
			Description: "Show the context token count, its sources and the full-request breakdown",
			Handler:     a.tokensCommand,
		})
	}
	for _, name := range []string{"quotas", "quota"} {
		ext.RegisterCommand(name, sdk.CommandOptions{
			Description:            "Show quotas; `reset [provider]` spends a reset credit after asking",
			GetArgumentCompletions: a.quotaCompletions,
			Handler:                a.quotasCommand,
		})
	}
}

// resolver maps a provider name to its quota source id; before the first session it knows no one.
func (a *App) resolver() command.Resolver {
	st := a.session()
	if st == nil || st.quotas == nil {
		return func(string) string { return "" }
	}
	return func(name string) string {
		if s := st.quotas.Registry().Resolve(name); s != nil {
			return s.ID
		}
		return ""
	}
}

func settingKeys() []string {
	keys := make([]string, len(config.Catalog))
	for i, d := range config.Catalog {
		keys[i] = d.Key
	}
	return keys
}

func (a *App) providerNames() []string {
	st := a.session()
	if st == nil || st.quotas == nil {
		return nil
	}
	var out []string
	for _, s := range st.quotas.Registry().All() {
		out = append(out, s.ID)
		out = append(out, s.Aliases...)
	}
	return out
}

// items builds completion rows. The host replaces the whole argument text with an item's
// value, so lead is the words already typed before the one being completed.
func items(lead string, values []string) []sdk.AutocompleteItem {
	out := make([]sdk.AutocompleteItem, len(values))
	for i, v := range values {
		out[i] = sdk.AutocompleteItem{Value: lead + v, Label: v}
	}
	return out
}

func sectionWords() []string {
	var out []string
	for _, s := range command.Sections {
		out = append(out, string(s))
	}
	return out
}

func (a *App) authCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	f := strings.Fields(prefix)
	if len(f) == 0 || (len(f) == 1 && !strings.HasSuffix(prefix, " ")) {
		return items("", command.Complete(strings.TrimSpace(prefix), sectionWords())), nil
	}
	arg := ""
	if len(f) > 1 {
		arg = f[1]
	}
	switch strings.ToLower(f[0]) {
	case "quotas", "quota", "usage", "limits":
		return items(f[0]+" ", command.Complete(arg, a.providerNames())), nil
	case "settings", "setting", "config", "options":
		return items(f[0]+" ", command.Complete(arg, settingKeys())), nil
	}
	return nil, nil
}

func (a *App) quotaCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	f := strings.Fields(prefix)
	trailing := strings.HasSuffix(prefix, " ")
	switch {
	case len(f) == 0 || (len(f) == 1 && !trailing):
		return items("", command.Complete(strings.TrimSpace(prefix), append([]string{"reset"}, a.providerNames()...))), nil
	case len(f) == 1 || (len(f) == 2 && !trailing):
		if strings.EqualFold(f[0], "reset") || strings.EqualFold(f[0], "resets") {
			arg := ""
			if len(f) == 2 {
				arg = f[1]
			}
			return items(f[0]+" ", command.Complete(arg, a.providerNames())), nil
		}
	}
	return nil, nil
}

func (a *App) authCommand(ctx sdk.Context, args string) error {
	req, err := command.Auth(args, a.resolver(), settingKeys())
	if err != nil {
		ctx.Notify(err.Error(), "warning")
		return nil
	}
	return a.run(ctx, req)
}

func (a *App) tokensCommand(ctx sdk.Context, args string) error {
	req, err := command.Tokens(args)
	if err != nil {
		ctx.Notify(err.Error(), "warning")
		return nil
	}
	return a.run(ctx, req)
}

func (a *App) quotasCommand(ctx sdk.Context, args string) error {
	req, err := command.Quotas(args, a.resolver())
	if err != nil {
		ctx.Notify(err.Error(), "warning")
		return nil
	}
	return a.run(ctx, req)
}

// run executes a parsed request: reset flow, interactive dashboard, or headless text.
func (a *App) run(ctx sdk.Context, req command.Request) error {
	st := a.session()
	if st == nil {
		ctx.Notify("pig-auth-plus is not active in this session.", "warning")
		return nil
	}
	if req.Reset {
		return a.resetCommand(ctx, st, req.Provider)
	}
	if ctx.Mode() == "tui" && ctx.HasUI() {
		return a.openDashboard(ctx, string(req.Section), req.Provider, req.Key)
	}
	return a.textCommand(ctx, st, req)
}

func (a *App) textCommand(ctx sdk.Context, st *state, req command.Request) error {
	switch req.Section {
	case command.SectionQuotas:
		ctx.Notify(uiquotas.PlainText(st.quotas.View(), a.display(), req.Provider, headlessWidth, time.Now()), "info")
	case command.SectionProviders:
		ctx.Notify(uiproviders.PlainText(a.providerRows(ctx, time.Now()), headlessWidth, time.Now()), "info")
	case command.SectionTokens:
		ctx.Notify(uitokens.PlainText(st.tokens.Latest(), a.tokenDisplay(), headlessWidth), "info")
	case command.SectionSettings:
		ctx.Notify(uisettings.PlainText(st.settings.Snapshot(), headlessWidth, req.Key), "info")
	default:
		ctx.Notify("The "+string(req.Section)+" view needs the interactive TUI.", "info")
	}
	return nil
}

// uiDialogs adapts the SDK's Select to the reset flow; it is non-interactive without a TUI.
type uiDialogs struct{ ctx sdk.Context }

func (d uiDialogs) Interactive() bool { return d.ctx.Mode() == "tui" && d.ctx.HasUI() }

func (d uiDialogs) Select(title string, options []string) (string, bool, error) {
	v, ok, err := d.ctx.Select(title, options)
	if err != nil || !ok {
		return "", false, err
	}
	s, _ := v.(string)
	return s, s != "", nil
}

func (a *App) resetCommand(ctx sdk.Context, st *state, provider string) error {
	res, err := resetflow.Run(st.group.Context(), st.quotas, uiDialogs{ctx}, provider, quota.FromCommand)
	if err != nil {
		ctx.Notify("Reset: "+err.Error(), "error")
		return nil
	}
	level := "info"
	if res.Warn {
		level = "warning"
	}
	ctx.Notify(res.Text, level)
	return nil
}
