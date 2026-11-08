// Package models is the Models dashboard tab: the model catalog with search and per-model
// detail, and the entry point to overrides and aliases.
package models

import (
	"fmt"
	"strings"

	"github.com/furbyhaxx/pig-auth-plus/internal/line"
)

// Cost is the price per million tokens.
type Cost struct{ Input, Output, CacheRead, CacheWrite float64 }

// Model is one catalog entry as the tab shows it.
type Model struct {
	Provider      string
	ID            string
	Name          string
	API           string
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
	Input         []string
	Cost          Cost
	// AliasOf is the source model id when this entry is an alias of another model.
	AliasOf string
	// Overridden lists the fields a models.json override changes.
	Overridden []string
}

// Key is provider/id.
func (m Model) Key() string { return m.Provider + "/" + m.ID }

// Label is the display name, falling back to the id.
func (m Model) Label() string {
	if n := strings.TrimSpace(line.Plain(m.Name)); n != "" {
		return n
	}
	return line.Plain(m.ID)
}

// Tokens formats a token count compactly: 1200 -> 1.2k, 1000000 -> 1M, 0 -> ?.
func Tokens(n int) string {
	switch {
	case n <= 0:
		return "?"
	case n >= 1_000_000 && n%100_000 == 0:
		return trim(float64(n)/1e6) + "M"
	case n >= 1000:
		return trim(float64(n)/1e3) + "k"
	}
	return fmt.Sprint(n)
}

func trim(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".")
}

// Price formats a price per million tokens.
func Price(v float64) string {
	if v == 0 {
		return "$0"
	}
	return "$" + trim(v)
}

// match reports whether every word of query occurs in the model's provider, id or name.
func match(m Model, query string) bool {
	hay := strings.ToLower(m.Provider + "/" + m.ID + " " + m.Name)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}
