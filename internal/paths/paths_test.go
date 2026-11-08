package paths

import "testing"

func TestResolveXDG(t *testing.T) {
	env := map[string]string{"HOME": "/h", "XDG_STATE_HOME": "relative", "XDG_CACHE_HOME": "/c"}
	p, err := Resolve("/a", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if p.StateDir != "/h/.local/state/pig-auth-plus" {
		t.Errorf("relative XDG_STATE_HOME must be ignored, got %q", p.StateDir)
	}
	if p.CacheDir != "/c/pig-auth-plus" {
		t.Errorf("cache dir %q", p.CacheDir)
	}
	if p.UserConfig() != "/a/auth-plus.toml" {
		t.Errorf("user config %q", p.UserConfig())
	}
}

func TestResolveRequiresAgentDir(t *testing.T) {
	if _, err := Resolve("", func(string) string { return "" }); err == nil {
		t.Fatal("expected error without agent dir")
	}
}
