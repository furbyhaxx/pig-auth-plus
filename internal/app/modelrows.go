package app

import (
	"sort"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/fsx"
	"github.com/furbyhaxx/pig-auth-plus/internal/modelsfile"
	uimodels "github.com/furbyhaxx/pig-auth-plus/internal/ui/models"
)

// catalogRows reads every model the host lists. It does I/O: call it off the render path.
func (a *App) catalogRows(host sdk.Context) ([]uimodels.Model, error) {
	all, err := host.ModelRegistry().GetAll()
	if err != nil {
		return nil, err
	}
	var overrides map[string]map[string]map[string]any
	if st := a.session(); st != nil {
		if data, err := fsx.ReadOptional(st.paths.Models()); err == nil {
			overrides, _ = modelsfile.ReadAll(data)
		}
	}
	aliases := map[string]string{}
	for _, al := range a.settings().Aliases {
		aliases[al.Provider+"/"+al.ID] = al.Source
	}
	out := make([]uimodels.Model, 0, len(all))
	for _, m := range all {
		row := uimodels.Model{
			Provider:      str(m["provider"]),
			ID:            str(m["id"]),
			Name:          str(m["name"]),
			API:           str(m["api"]),
			ContextWindow: num(m["contextWindow"]),
			MaxTokens:     num(m["maxTokens"]),
		}
		row.Reasoning, _ = m["reasoning"].(bool)
		if in, ok := m["input"].([]any); ok {
			for _, v := range in {
				row.Input = append(row.Input, str(v))
			}
		}
		if c, ok := m["cost"].(map[string]any); ok {
			row.Cost = uimodels.Cost{Input: fnum(c["input"]), Output: fnum(c["output"]), CacheRead: fnum(c["cacheRead"]), CacheWrite: fnum(c["cacheWrite"])}
		}
		row.AliasOf = aliases[row.Provider+"/"+row.ID]
		row.Overridden = modelsfile.Keys(overrides[row.Provider][row.ID])
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func str(v any) string { s, _ := v.(string); return s }

func fnum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case interface{ Float64() (float64, error) }:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func num(v any) int { return int(fnum(v)) }
