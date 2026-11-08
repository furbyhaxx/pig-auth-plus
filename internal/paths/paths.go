// Package paths resolves every filesystem location pig-auth-plus reads or writes.
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// App is the directory name under the XDG state base.
const App = "pig-auth-plus"

// Paths holds resolved absolute locations. Construct with [Resolve] or literally in tests.
type Paths struct {
	// AgentDir is the host's agent directory; credentials, models and the user config live here.
	AgentDir string
	// StateDir holds one-time migration markers and device identities.
	StateDir string
}

// Env reads one environment variable; tests replace it.
type Env func(string) string

// Resolve builds Paths from the host-reported agent directory and the XDG state base.
func Resolve(agentDir string, env Env) (Paths, error) {
	if agentDir == "" {
		return Paths{}, errors.New("host did not report an agent directory")
	}
	home := env("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, errors.New("cannot resolve the home directory for XDG defaults")
		}
		home = h
	}
	return Paths{
		AgentDir: agentDir,
		StateDir: filepath.Join(xdg(env, "XDG_STATE_HOME", home, ".local/state"), App),
	}, nil
}

// xdg follows the XDG spec: a relative base directory is invalid and ignored.
func xdg(env Env, key, home, fallback string) string {
	if v := env(key); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home, fallback)
}

// UserConfig is the user settings file.
func (p Paths) UserConfig() string { return filepath.Join(p.AgentDir, "auth-plus.toml") }

// LegacyConfig is pi-auth-plus' settings file, imported once.
func (p Paths) LegacyConfig() string { return filepath.Join(p.AgentDir, "auth-plus.json") }

// QuotaCache is the host-shared quota cache.
func (p Paths) QuotaCache() string {
	return filepath.Join(p.AgentDir, "cache", "pig-auth-plus", "quotas.json")
}

// CodexLegacyAuth is pi-quotas' Codex credential file, read as a fallback.
func (p Paths) CodexLegacyAuth() string { return filepath.Join(p.AgentDir, "quotas-codex-auth.json") }

// Models is the host's user model overrides file.
func (p Paths) Models() string { return filepath.Join(p.AgentDir, "models.json") }

// Migrations is the completed one-time import marker file.
func (p Paths) Migrations() string { return filepath.Join(p.StateDir, "migrations.json") }

// Wire is the private directory of the provider wire runtime; it never holds user credentials.
func (p Paths) Wire() string { return filepath.Join(p.StateDir, "wire") }
