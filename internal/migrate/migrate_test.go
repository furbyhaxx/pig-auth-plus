package migrate

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/furbyhaxx/pig-auth-plus/internal/config"
	"github.com/furbyhaxx/pig-auth-plus/internal/paths"
)

func TestImportFillsUnsetOnce(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{AgentDir: dir, StateDir: filepath.Join(dir, "state")}
	legacy := `{"quotas":{"providers":["claude"],"creditHealthyUsd":10,"linePlacement":"off"},
		"openai":{"verbosity":"low"},"models":{"rules":["x/*"],"groups":[],
		"aliases":[{"provider":"p","source":"s","id":"a"}]},"synthetic":{"webSearch":true}}`
	os.WriteFile(p.LegacyConfig(), []byte(legacy), 0o600)
	os.WriteFile(p.UserConfig(), []byte("[quotas]\nlinePlacement = \"above-editor\"\n"), 0o600)
	m := config.NewManager(p.UserConfig())
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	res, err := ImportLegacySettings(context.Background(), p, m)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Skipped, []string{"models.groups", "models.rules", "synthetic.webSearch"}) {
		t.Errorf("skipped %v", res.Skipped)
	}
	s := m.Snapshot()
	if s.LinePlacement != "above-editor" {
		t.Errorf("existing TOML value must win, got %q", s.LinePlacement)
	}
	if s.Verbosity != "low" || s.CreditHealthyUSD != 10 || len(s.Aliases) != 1 || !slices.Equal(s.QuotaProviders, []string{"claude"}) {
		t.Errorf("import incomplete: %+v", s.Settings)
	}
	os.WriteFile(p.LegacyConfig(), []byte(`{"openai":{"verbosity":"high"}}`), 0o600)
	m.Save(context.Background(), config.ScopeUser, []config.Edit{{Key: "openai.verbosity", Reset: true}})
	if res, err := ImportLegacySettings(context.Background(), p, m); err != nil || res.Done {
		t.Errorf("second import must not run: %+v %v", res, err)
	}
}

func TestMalformedLegacyIsRetried(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{AgentDir: dir, StateDir: filepath.Join(dir, "state")}
	os.WriteFile(p.LegacyConfig(), []byte("{"), 0o600)
	m := config.NewManager(p.UserConfig())
	if _, err := ImportLegacySettings(context.Background(), p, m); err == nil {
		t.Fatal("malformed legacy file must be reported")
	}
	if _, err := os.Stat(p.Migrations()); !os.IsNotExist(err) {
		t.Error("no marker may be written for a failed import")
	}
}
