package config

import "slices"

// Settings is the typed, resolved view of every setting.
type Settings struct {
	Density        string
	MaxBodyRows    int
	ShowProvenance bool

	RefreshIntervalSec int
	RefreshOnRequest   bool
	QuotaProviders     []string
	LinePlacement      string
	PercentDisplay     string
	CompactWidthCols   int
	AlwaysShortIDs     bool
	HideSignedOut      bool
	CreditHealthyUSD   int
	CreditCriticalUSD  int
	LineTemplate       string

	Aliases []Alias

	Verbosity          string
	VerbosityProviders []string

	ReplacePatterns         []ReplacePattern
	CacheRetention          string
	CacheRetentionProviders []string

	TokensWarningPercent int
	TokensErrorPercent   int
}

func project(v map[string]any) Settings {
	i := func(k string) int { return int(v[k].(int64)) }
	b := func(k string) bool { return v[k].(bool) }
	s := func(k string) string { return v[k].(string) }
	l := func(k string) []string { return slices.Clone(v[k].([]string)) }
	return Settings{
		Density:                 s("dashboard.density"),
		MaxBodyRows:             i("dashboard.maxBodyRows"),
		ShowProvenance:          b("dashboard.showProvenance"),
		RefreshIntervalSec:      i("quotas.refreshIntervalSec"),
		RefreshOnRequest:        b("quotas.refreshOnRequest"),
		QuotaProviders:          l("quotas.providers"),
		LinePlacement:           s("quotas.linePlacement"),
		PercentDisplay:          s("quotas.percentDisplay"),
		CompactWidthCols:        i("quotas.compactWidthCols"),
		AlwaysShortIDs:          b("quotas.alwaysShortIds"),
		HideSignedOut:           b("quotas.hideSignedOut"),
		CreditHealthyUSD:        i("quotas.creditHealthyUsd"),
		CreditCriticalUSD:       i("quotas.creditCriticalUsd"),
		LineTemplate:            s("quotas.lineTemplate"),
		Aliases:                 slices.Clone(v["models.aliases"].([]Alias)),
		Verbosity:               s("openai.verbosity"),
		VerbosityProviders:      l("openai.verbosityProviders"),
		ReplacePatterns:         slices.Clone(v["claude.replacePatterns"].([]ReplacePattern)),
		CacheRetention:          s("claude.cacheRetention"),
		CacheRetentionProviders: l("claude.cacheRetentionProviders"),
		TokensWarningPercent:    i("tokens.warningPercent"),
		TokensErrorPercent:      i("tokens.errorPercent"),
	}
}
