package config

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Kind is the value shape of a setting.
type Kind int

// Setting kinds.
const (
	KindBool Kind = iota
	KindInt
	KindEnum
	KindString
	KindStringList
	KindAliases
	KindReplacePatterns
)

// Definition describes one setting: its key, default, accepted values and help text.
type Definition struct {
	Key     string
	Kind    Kind
	Default any
	Min     int64
	Max     int64
	Enum    []string
	Help    string
}

// Alias is a provider-local model alias that sends requests with the source model id.
type Alias struct {
	Provider      string
	Source        string
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
}

// ReplacePattern rewrites Claude system-prompt text; without a replacement it drops the
// paragraphs that contain Match.
type ReplacePattern struct {
	Match          string
	Replacement    string
	HasReplacement bool
}

// DefaultLineTemplate is the built-in quota line template.
const DefaultLineTemplate = "{openrouter:id}: {openrouter:credits-remaining} · {deepseek:id}: {deepseek:credits-remaining} · " +
	"{codex:id}: {codex:window-7d-used-percent}[ ({codex:resets-available}r)] · " +
	"{claude:id}: 5h/{claude:window-5h-used-percent} 7d/{claude:window-7d-used-percent}[ ({claude:resets-available}r)] · " +
	"{antigravity:id}: gem/{antigravity:window-7d-gemini-used-percent} arg/{antigravity:window-7d-gemini-4-argon-used-percent} · " +
	"{synthetic:id}: {synthetic:window-7d-used-percent}[ ({synthetic:resets-available}r)] · " +
	"{xai:id}: {xai:window-7d-used-percent}[ ({xai:resets-available}r)]"

// Catalog lists every setting in display order.
var Catalog = []Definition{
	{Key: "dashboard.density", Kind: KindEnum, Default: "comfortable", Enum: []string{"comfortable", "compact"},
		Help: "Dashboard row spacing; compact uses less vertical space."},
	{Key: "dashboard.maxBodyRows", Kind: KindInt, Default: int64(0), Min: 0, Max: 60,
		Help: "Caps dashboard body rows; 0 fits the available space."},
	{Key: "dashboard.showProvenance", Kind: KindBool, Default: false,
		Help: "Shows alias sources and per-field override/default labels in model detail."},
	{Key: "quotas.refreshIntervalSec", Kind: KindInt, Default: int64(60), Min: 15, Max: 3600,
		Help: "Seconds before a quota endpoint is polled again; provider floors still apply."},
	{Key: "quotas.refreshOnRequest", Kind: KindBool, Default: true,
		Help: "Refresh the active provider's quota after each turn, subject to its floor."},
	{Key: "quotas.providers", Kind: KindStringList, Default: []string{},
		Help: "Quota sources to poll, by id or alias; empty means all."},
	{Key: "quotas.linePlacement", Kind: KindEnum, Default: "below-editor-first",
		Enum: []string{"above-editor", "below-editor-first", "below-editor-last", "off"},
		Help: "Where the quota line is shown; off hides it but polling continues."},
	{Key: "quotas.percentDisplay", Kind: KindEnum, Default: "used", Enum: []string{"used", "remaining"},
		Help: "Whether broad line metrics and panel percentages show used or remaining allowance."},
	{Key: "quotas.compactWidthCols", Kind: KindInt, Default: int64(100), Min: 40, Max: 220,
		Help: "Below this terminal width, line identities and summaries shorten."},
	{Key: "quotas.alwaysShortIds", Kind: KindBool, Default: false,
		Help: "Use short line identities at every width."},
	{Key: "quotas.hideSignedOut", Kind: KindBool, Default: true,
		Help: "Omit providers without a credential from the quota line, tab and summaries."},
	{Key: "quotas.creditHealthyUsd", Kind: KindInt, Default: int64(50), Min: 5, Max: 500,
		Help: "A balance at or above this is shown as healthy. Display only."},
	{Key: "quotas.creditCriticalUsd", Kind: KindInt, Default: int64(5), Min: 0, Max: 100,
		Help: "A balance at or below this is shown as critical. Display only."},
	{Key: "quotas.lineTemplate", Kind: KindString, Default: DefaultLineTemplate,
		Help: "Quota line template: literal text and {provider:field} tokens."},
	{Key: "models.aliases", Kind: KindAliases, Default: []Alias{},
		Help: "Provider-local model aliases; requests use the source model id."},
	{Key: "openai.verbosity", Kind: KindEnum, Default: "default", Enum: []string{"default", "low", "medium", "high"},
		Help: "Response verbosity for OpenAI Responses requests; default leaves requests unchanged."},
	{Key: "openai.verbosityProviders", Kind: KindStringList, Default: []string{"openai", "openai-codex"},
		Help: "Providers the verbosity override applies to; empty disables it."},
	{Key: "claude.replacePatterns", Kind: KindReplacePatterns, Default: []ReplacePattern{},
		Help: "Literal system-prompt rewrites for Claude subscription requests; no replacement drops the paragraph."},
	{Key: "claude.cacheRetention", Kind: KindEnum, Default: "default", Enum: []string{"default", "short", "long"},
		Help: "Prompt cache lifetime for Anthropic Messages breakpoints: short is 5 minutes, long 1 hour."},
	{Key: "claude.cacheRetentionProviders", Kind: KindStringList, Default: []string{"claude", "anthropic"},
		Help: "Providers the cache retention override applies to; empty disables it."},
	{Key: "tokens.warningPercent", Kind: KindInt, Default: int64(70), Min: 0, Max: 100,
		Help: "Context usage at or above this percentage is shown in warning color."},
	{Key: "tokens.errorPercent", Kind: KindInt, Default: int64(90), Min: 0, Max: 100,
		Help: "Context usage at or above this percentage is shown in error color."},
}

// relation requires the Lower setting to be strictly below the Upper setting.
type relation struct{ Lower, Upper string }

var relations = []relation{
	{"quotas.creditCriticalUsd", "quotas.creditHealthyUsd"},
	{"tokens.warningPercent", "tokens.errorPercent"},
}

var definitions = func() map[string]*Definition {
	m := make(map[string]*Definition, len(Catalog))
	for i := range Catalog {
		m[Catalog[i].Key] = &Catalog[i]
	}
	return m
}()

// Lookup returns the definition of key.
func Lookup(key string) (*Definition, bool) {
	d, ok := definitions[key]
	return d, ok
}

// Validate normalizes a decoded TOML value (or a Go value of the setting's type).
// Out-of-range integers are clamped and reported through clamped.
func (d *Definition) Validate(value any) (normalized any, clamped bool, err error) {
	switch d.Kind {
	case KindBool:
		b, ok := value.(bool)
		if !ok {
			return nil, false, errors.New("expected true or false")
		}
		return b, false, nil
	case KindInt:
		n, ok := toInt(value)
		if !ok {
			return nil, false, errors.New("expected an integer")
		}
		c := min(max(n, d.Min), d.Max)
		return c, c != n, nil
	case KindEnum:
		s, ok := value.(string)
		if !ok || !slices.Contains(d.Enum, s) {
			return nil, false, fmt.Errorf("expected one of %s", strings.Join(d.Enum, ", "))
		}
		return s, false, nil
	case KindString:
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, false, errors.New("expected a nonblank string")
		}
		return s, false, nil
	case KindStringList:
		list, err := stringList(value)
		return list, false, err
	case KindAliases:
		list, err := aliases(value)
		return list, false, err
	case KindReplacePatterns:
		list, err := replacePatterns(value)
		return list, false, err
	}
	return nil, false, errors.New("unknown setting kind")
}

func toInt(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return int64(math.Round(v)), true
	}
	return 0, false
}

func stringList(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return slices.Clone(v), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("expected a list of strings")
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}
	return nil, errors.New("expected a list of strings")
}

func tables(value any) ([]map[string]any, error) {
	switch v := value.(type) {
	case []map[string]any:
		return v, nil
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			t, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("expected a list of tables")
			}
			out = append(out, t)
		}
		return out, nil
	}
	return nil, errors.New("expected a list of tables")
}

func aliases(value any) ([]Alias, error) {
	if v, ok := value.([]Alias); ok {
		return slices.Clone(v), nil
	}
	rows, err := tables(value)
	if err != nil {
		return nil, err
	}
	out := make([]Alias, 0, len(rows))
	seen := map[string]bool{}
	for i, row := range rows {
		a := Alias{}
		var okP, okS, okI bool
		a.Provider, okP = nonblank(row["provider"])
		a.Source, okS = nonblank(row["source"])
		a.ID, okI = nonblank(row["id"])
		if !okP || !okS || !okI {
			return nil, fmt.Errorf("alias %d needs nonblank provider, source and id", i+1)
		}
		if n, present := row["name"]; present {
			s, ok := n.(string)
			if !ok {
				return nil, fmt.Errorf("alias %d name must be a string", i+1)
			}
			a.Name = s
		}
		for field, dst := range map[string]*int64{"contextWindow": &a.ContextWindow, "maxTokens": &a.MaxTokens} {
			if raw, present := row[field]; present {
				n, ok := toInt(raw)
				if !ok || n <= 0 {
					return nil, fmt.Errorf("alias %d %s must be a positive integer", i+1, field)
				}
				*dst = n
			}
		}
		key := a.Provider + "/" + a.ID
		if seen[key] {
			return nil, fmt.Errorf("alias %s is defined twice", key)
		}
		seen[key] = true
		out = append(out, a)
	}
	return out, nil
}

func replacePatterns(value any) ([]ReplacePattern, error) {
	if v, ok := value.([]ReplacePattern); ok {
		return slices.Clone(v), nil
	}
	rows, err := tables(value)
	if err != nil {
		return nil, err
	}
	out := make([]ReplacePattern, 0, len(rows))
	for i, row := range rows {
		match, ok := row["match"].(string)
		if !ok || strings.TrimSpace(match) == "" {
			return nil, fmt.Errorf("pattern %d needs a nonblank match", i+1)
		}
		p := ReplacePattern{Match: match}
		if raw, present := row["replacement"]; present {
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("pattern %d replacement must be a string", i+1)
			}
			p.Replacement, p.HasReplacement = s, true
		}
		out = append(out, p)
	}
	return out, nil
}

func nonblank(v any) (string, bool) {
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && s != ""
}
