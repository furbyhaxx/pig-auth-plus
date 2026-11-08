// Package command parses the slash-command arguments of /auth, /quotas and /tokens into
// typed requests, and suggests nearby names for typos. It does no I/O.
package command

import (
	"fmt"
	"slices"
	"strings"
)

// Section is one dashboard tab.
type Section string

// Dashboard sections.
const (
	SectionProviders Section = "providers"
	SectionQuotas    Section = "quotas"
	SectionTokens    Section = "tokens"
	SectionModels    Section = "models"
	SectionSettings  Section = "settings"
)

// Sections lists every section in tab order.
var Sections = []Section{SectionProviders, SectionQuotas, SectionTokens, SectionModels, SectionSettings}

var sectionAliases = map[string]Section{
	"providers": SectionProviders, "provider": SectionProviders, "status": SectionProviders, "accounts": SectionProviders, "account": SectionProviders,
	"quotas": SectionQuotas, "quota": SectionQuotas, "usage": SectionQuotas, "limits": SectionQuotas,
	"tokens": SectionTokens, "token": SectionTokens, "count": SectionTokens, "counts": SectionTokens,
	"models": SectionModels, "model": SectionModels,
	"settings": SectionSettings, "setting": SectionSettings, "config": SectionSettings, "options": SectionSettings,
}

// Resolver maps a provider name or alias to its id; empty means unknown.
type Resolver func(name string) string

// Request is a parsed command.
type Request struct {
	Section Section
	// Provider is a resolved quota provider id for a solo Quotas view or a reset.
	Provider string
	// Key is a settings key to select.
	Key string
	// Reset asks for the reset-credit flow.
	Reset bool
}

// Error is a usage problem to show the user verbatim.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

const authUsage = "Usage: /auth [providers|quotas [provider]|tokens|models|settings [key]]"

// Auth parses the arguments of /auth.
func Auth(args string, resolve Resolver, settingKeys []string) (Request, error) {
	f := strings.Fields(args)
	if len(f) == 0 {
		return Request{Section: SectionProviders}, nil
	}
	sec, ok := sectionAliases[strings.ToLower(f[0])]
	if !ok {
		return Request{}, unknown("section", f[0], authUsage, sectionNames())
	}
	rest := f[1:]
	switch sec {
	case SectionQuotas:
		return quotasView(rest, resolve)
	case SectionSettings:
		if len(rest) > 1 {
			return Request{}, &Error{"Usage: /auth settings [key]"}
		}
		r := Request{Section: SectionSettings}
		if len(rest) == 1 {
			key := keyMatch(rest[0], settingKeys)
			if key == "" {
				return Request{}, unknown("setting", rest[0], "Usage: /auth settings [key]", settingKeys)
			}
			r.Key = key
		}
		return r, nil
	}
	if len(rest) > 0 {
		return Request{}, &Error{fmt.Sprintf("/auth %s takes no arguments. %s", sec, authUsage)}
	}
	return Request{Section: sec}, nil
}

const quotasUsage = "Usage: /quotas [provider] | /quotas reset [provider]"

// Quotas parses the arguments of /quotas and /quota.
func Quotas(args string, resolve Resolver) (Request, error) {
	f := strings.Fields(args)
	if len(f) > 0 {
		switch strings.ToLower(f[0]) {
		case "reset", "resets":
			if len(f) > 2 {
				return Request{}, &Error{"Usage: /quotas reset [provider]; extra words: " + strings.Join(f[2:], " ")}
			}
			r := Request{Section: SectionQuotas, Reset: true}
			if len(f) == 2 {
				id := resolve(f[1])
				if id == "" {
					return Request{}, unknown("provider", f[1], quotasUsage, nil)
				}
				r.Provider = id
			}
			return r, nil
		}
	}
	return quotasView(f, resolve)
}

func quotasView(f []string, resolve Resolver) (Request, error) {
	switch len(f) {
	case 0:
		return Request{Section: SectionQuotas}, nil
	case 1:
		if id := resolve(f[0]); id != "" {
			return Request{Section: SectionQuotas, Provider: id}, nil
		}
		return Request{}, unknown("provider", f[0], quotasUsage, nil)
	}
	return Request{}, &Error{quotasUsage}
}

// Tokens parses /tokens and /token, which take no arguments.
func Tokens(args string) (Request, error) {
	if strings.TrimSpace(args) != "" {
		return Request{}, &Error{"Usage: /tokens (takes no arguments)"}
	}
	return Request{Section: SectionTokens}, nil
}

func sectionNames() []string {
	out := make([]string, len(Sections))
	for i, s := range Sections {
		out[i] = string(s)
	}
	return out
}

func keyMatch(word string, keys []string) string {
	for _, k := range keys {
		if strings.EqualFold(k, word) {
			return k
		}
	}
	return ""
}

func unknown(kind, word, usage string, names []string) *Error {
	msg := fmt.Sprintf("Unknown %s %q.", kind, plain(word))
	if near := Nearby(word, names); len(near) > 0 {
		msg += " Did you mean " + strings.Join(near, ", ") + "?"
	}
	return &Error{msg + " " + usage}
}

// Nearby returns up to three names that start with, contain or are one or two edits from word.
func Nearby(word string, names []string) []string {
	w := strings.ToLower(word)
	type scored struct {
		name  string
		score int
	}
	var hits []scored
	for _, n := range names {
		l := strings.ToLower(n)
		switch {
		case strings.HasPrefix(l, w) || strings.HasPrefix(w, l):
			hits = append(hits, scored{n, 0})
		case strings.Contains(l, w):
			hits = append(hits, scored{n, 1})
		default:
			if d := edit(l, w); d <= 2 {
				hits = append(hits, scored{n, 1 + d})
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return a.score - b.score })
	var out []string
	for _, h := range hits {
		if len(out) == 3 {
			break
		}
		out = append(out, h.name)
	}
	return out
}

// plain drops control characters from user text echoed into a message.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func edit(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// Complete returns the candidates that start with prefix, case-insensitively.
func Complete(prefix string, candidates []string) []string {
	p := strings.ToLower(prefix)
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), p) {
			out = append(out, c)
		}
	}
	return out
}
