// Package migrate performs one-time carry-overs from pi-auth-plus.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/fsx"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
)

// legacySettingsMarker names the completed auth-plus.json import in the markers file.
const legacySettingsMarker = "auth-plus.json"

// Result describes what an import did; Done is false when nothing ran.
type Result struct {
	Done     bool
	Imported []string
	Skipped  []string
	Warnings []string
}

// ImportLegacySettings copies pi-auth-plus' auth-plus.json into auth-plus.toml once: only
// keys the TOML file does not set, only settings this extension still has. A malformed
// legacy file is reported and retried on a later start.
func ImportLegacySettings(ctx context.Context, p paths.Paths, m *config.Manager) (Result, error) {
	markers, err := readMarkers(p.Migrations())
	if err != nil {
		return Result{}, err
	}
	if _, done := markers[legacySettingsMarker]; done {
		return Result{}, nil
	}
	data, err := fsx.ReadOptional(p.LegacyConfig())
	if err != nil || data == nil {
		return Result{}, err
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return Result{}, fmt.Errorf("%s is not valid JSON; fix it to import its settings", p.LegacyConfig())
	}
	res := Result{Done: true}
	var edits []config.Edit
	flat := map[string]any{}
	flatten("", tree, flat)
	for _, key := range slices.Sorted(maps.Keys(flat)) {
		def, ok := config.Lookup(key)
		if !ok {
			res.Skipped = append(res.Skipped, key)
			continue
		}
		v, clamped, err := def.Validate(flat[key])
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v; not imported", key, err))
			continue
		}
		if clamped {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s clamped to %d–%d", key, def.Min, def.Max))
		}
		edits = append(edits, config.Edit{Key: key, Value: v})
	}
	if len(edits) > 0 {
		if res.Imported, err = m.FillUnset(ctx, edits); err != nil {
			return Result{}, fmt.Errorf("import %s: %w", p.LegacyConfig(), err)
		}
	}
	return res, writeMarker(ctx, p.Migrations(), legacySettingsMarker)
}

// flatten turns nested objects into dotted keys; lists and scalars are leaves.
func flatten(prefix string, tree map[string]any, out map[string]any) {
	for k, v := range tree {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			flatten(key, sub, out)
		} else {
			out[key] = v
		}
	}
}

func readMarkers(path string) (map[string]string, error) {
	data, err := fsx.ReadOptional(path)
	if err != nil {
		return nil, err
	}
	markers := map[string]string{}
	if data != nil {
		if err := json.Unmarshal(data, &markers); err != nil {
			return nil, errors.New("migration markers are corrupt; delete " + path + " to rerun imports")
		}
	}
	return markers, nil
}

func writeMarker(ctx context.Context, path, name string) error {
	unlock, err := fsx.Lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	markers, err := readMarkers(path)
	if err != nil {
		return err
	}
	markers[name] = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(markers, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(path, append(data, '\n'))
}
