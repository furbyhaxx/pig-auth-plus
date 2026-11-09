package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
	uimodels "github.com/furbyhaxx/pig-auth-plus/internal/ui/models"
	uiproviders "github.com/furbyhaxx/pig-auth-plus/internal/ui/providers"
	uiquotas "github.com/furbyhaxx/pig-auth-plus/internal/ui/quotas"
)

// providerExtra supplies each provider's plan, headline quota and detail rows from cached
// state; it does no I/O, so the dashboard can call it on every render. models may be nil.
func (a *App) providerExtra(st *state, models func() []uimodels.Model) func(id string) uiproviders.Extra {
	return func(id string) uiproviders.Extra {
		var ex uiproviders.Extra
		if st != nil {
			if v := viewOf(st.quotas.View(), id); v != nil {
				ex.Headline, ex.Plan = headline(v, a.display()), planOf(v)
				ex.Fields = append(ex.Fields, uiproviders.Field{Label: "Quota", Value: quotaDetail(v, a.display()), Jump: "quotas"})
			}
		}
		if models != nil {
			ex.Fields = append(ex.Fields, uiproviders.Field{Label: "Models", Value: modelCount(models(), id), Jump: "models"})
		}
		if v := tokenSource(id); v != "" {
			ex.Fields = append(ex.Fields, uiproviders.Field{Label: "Tokens", Value: v, Jump: "tokens"})
		}
		if v := a.settingsSummary(id); v != "" {
			ex.Fields = append(ex.Fields, uiproviders.Field{Label: "Settings", Value: v, Jump: "settings"})
		}
		return ex
	}
}

func viewOf(views []quota.View, id string) *quota.View {
	for i := range views {
		if views[i].Source != nil && views[i].Source.ID == id {
			return &views[i]
		}
	}
	return nil
}

func planOf(v *quota.View) string {
	if v.Snapshot == nil {
		return ""
	}
	return v.Snapshot.Tier
}

// headline is the compact quota summary of a row: the important windows, or the balance.
// Unknown stays "?" and a provider with no data has no headline.
func headline(v *quota.View, d uiquotas.Display) string {
	s := v.Snapshot
	if s == nil || s.NeedsAuth || (len(s.Windows) == 0 && s.Credits == nil) {
		return ""
	}
	cfg := line.Config{PercentUsed: d.PercentUsed}
	var parts []string
	if s.Credits != nil && (v.Source.Credits || len(s.Windows) == 0) {
		if s.Credits.Remaining != nil {
			parts = append(parts, fmt.Sprintf("%s%.2f left", s.Credits.Currency, *s.Credits.Remaining))
		}
	} else {
		for _, w := range line.ImportantWindows(s) {
			short := w.Short
			if short == "" {
				short = w.ID
			}
			parts = append(parts, short+" "+line.FormatPercent(w.Used, cfg))
		}
	}
	return strings.Join(parts, " · ")
}

func quotaDetail(v *quota.View, d uiquotas.Display) string {
	switch {
	case v.Snapshot == nil && v.FetchedAt == 0:
		return "not fetched yet"
	case v.Snapshot != nil && v.Snapshot.NeedsAuth:
		return "needs a login"
	case v.Snapshot != nil && v.Snapshot.Error != "" && headline(v, d) == "":
		return "error: " + v.Snapshot.Error
	}
	if h := headline(v, d); h != "" {
		return h
	}
	return "– nothing reported"
}

func modelCount(all []uimodels.Model, provider string) string {
	n, aliases := 0, 0
	for _, m := range all {
		if m.Provider != provider {
			continue
		}
		n++
		if m.AliasOf != "" {
			aliases++
		}
	}
	if n == 0 {
		return "none listed"
	}
	return strconv.Itoa(n) + " listed · " + strconv.Itoa(aliases) + " alias" + map[bool]string{true: "es", false: ""}[aliases != 1]
}

// tokenSource says how context is counted for the provider's models.
func tokenSource(id string) string {
	switch id {
	case "claude":
		return "provider usage; native count endpoint without an anchor"
	case "synthetic":
		return "provider usage in real tokens (measured per-model factors)"
	case "antigravity":
		return "provider usage; character estimate for the tail"
	}
	return ""
}

func (a *App) settingsSummary(id string) string {
	s := a.settings()
	switch id {
	case "claude":
		return fmt.Sprintf("cache retention %s · %d prompt rules", s.CacheRetention, len(s.ReplacePatterns))
	case "codex":
		return "response verbosity " + s.Verbosity
	}
	return ""
}
