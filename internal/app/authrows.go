package app

import (
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/quota/sources"
	uiproviders "github.com/furbyhaxx/pig-auth-plus/internal/ui/providers"
)

type providerEntry struct {
	id, label, login, note string
}

// providerEntries lists every provider the dashboard reports, in display order.
var providerEntries = []providerEntry{
	{"claude", "Claude", "/login claude", ""},
	{"antigravity", "Antigravity", "/login antigravity", ""},
	{"synthetic", "Synthetic", "/login synthetic", ""},
	{"codex", "ChatGPT", "/login openai-codex", "quota only; inference uses PiG's openai-codex"},
	{"xai", "xAI", "/login xai", "quota only; needs a Grok billing credential"},
	{"openrouter", "OpenRouter", "/login openrouter", "quota only"},
	{"deepseek", "DeepSeek", "/login deepseek", "quota only"},
	{"opencode", "OpenCode Zen", "/login opencode", "PiG builtin; no quota source"},
	{"opencode-go", "OpenCode Go", "/login opencode-go", "PiG builtin; no quota source"},
}

// providerRows reads credential state from the host. It does I/O: call it off the render path.
func (a *App) providerRows(host sdk.Context, now time.Time) []uiproviders.Row {
	st := a.session()
	rows := make([]uiproviders.Row, 0, len(providerEntries))
	for _, e := range providerEntries {
		row := uiproviders.Row{ID: e.id, Label: e.label, Login: e.login, Note: e.note}
		id := e.id
		if id == "codex" {
			id = sources.CodexPiProvider
		}
		status, err := host.ModelRegistry().GetProviderAuthStatus(id)
		switch {
		case err != nil:
			row.State, row.Err = uiproviders.Unknown, err.Error()
		case status.Configured:
			row.State, row.Source = uiproviders.SignedIn, sourceLabel(status, id)
			row.Expires = oauthExpiry(id)
		case e.id == "codex" && st != nil && sources.CodexFileAt(st.paths.CodexLegacyAuth())(now) != nil:
			row.State, row.Source, row.Login = uiproviders.SignedIn, "quotas-codex-auth.json", ""
		default:
			row.State = uiproviders.SignedOut
		}
		rows = append(rows, row)
	}
	return rows
}

// oauthExpiry is the stored OAuth access-token expiry; zero for other credentials.
func oauthExpiry(id string) time.Time {
	cred, err := sdk.ReadStoredCredential(id)
	if err != nil || cred == nil || cred.String("type") != "oauth" {
		return time.Time{}
	}
	v, ok := cred.Get("expires")
	if !ok {
		return time.Time{}
	}
	var ms float64
	switch n := v.(type) {
	case float64:
		ms = n
	case int64:
		ms = float64(n)
	case interface{ Float64() (float64, error) }:
		ms, _ = n.Float64()
	}
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms))
}

// sourceLabel names the credential kind or the environment variable behind a signed-in state.
func sourceLabel(status sdk.ProviderAuthStatus, id string) string {
	if status.Source == "stored" || status.Source == "" {
		switch cred, _ := sdk.ReadStoredCredential(id); {
		case cred != nil && cred.String("type") == "oauth":
			return "OAuth login"
		case cred != nil && cred.String("type") == "api_key":
			return "stored API key"
		}
	}
	if status.Label != "" {
		return status.Label
	}
	if status.Source == "" {
		return "configured"
	}
	return status.Source
}
