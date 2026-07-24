package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPathChoosesFirstExistingCandidate verifies precedence resolution picks the
// first candidate that actually exists, so a higher-precedence env var pointing
// at a directory without a config.toml does not shadow a real config lower down.
func TestPathChoosesFirstExistingCandidate(t *testing.T) {
	t.Parallel()

	pluginDir := t.TempDir() // highest precedence, but no config.toml written here
	home := t.TempDir()

	// Only the HOME candidate exists on disk.
	homeCfgDir := filepath.Join(home, ".config", "herdr-phone")
	if err := os.MkdirAll(homeCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeCfg := filepath.Join(homeCfgDir, "config.toml")
	if err := os.WriteFile(homeCfg, []byte("# home config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := envMap(map[string]string{
		"HERDR_PLUGIN_CONFIG_DIR": pluginDir, // set but empty of config.toml
		"HOME":                    home,
	})

	if got := Path(env); got != homeCfg {
		t.Errorf("Path = %q, want the existing home config %q", got, homeCfg)
	}

	// Now create the higher-precedence config; it must win.
	pluginCfg := filepath.Join(pluginDir, "config.toml")
	if err := os.WriteFile(pluginCfg, []byte("# plugin config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Path(env); got != pluginCfg {
		t.Errorf("Path = %q, want the higher-precedence plugin config %q", got, pluginCfg)
	}
}

// TestPathNoneExistFallsBackToHighestPrecedence verifies that when no candidate
// file exists, Path returns the highest-precedence candidate so callers report a
// sensible location and Load then applies its missing-file semantics.
func TestPathNoneExistFallsBackToHighestPrecedence(t *testing.T) {
	t.Parallel()
	env := envMap(map[string]string{
		"HERDR_PLUGIN_CONFIG_DIR": "/no/such/plugin",
		"XDG_CONFIG_HOME":         "/no/such/xdg",
		"HOME":                    "/no/such/home",
	})
	want := filepath.Join("/no/such/plugin", "config.toml")
	if got := Path(env); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// TestConfigFallbackLoadsLowerPrecedenceFile confirms Load actually reads the
// first existing candidate end to end.
func TestConfigFallbackLoadsLowerPrecedenceFile(t *testing.T) {
	t.Parallel()
	pluginDir := t.TempDir()
	home := t.TempDir()
	homeCfgDir := filepath.Join(home, ".config", "herdr-phone")
	if err := os.MkdirAll(homeCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A valid quick-mode config lives only in the HOME location.
	body := "[cloudflare]\nmode = \"quick\"\nquick_enabled = true\n"
	if err := os.WriteFile(filepath.Join(homeCfgDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	env := envMap(map[string]string{
		"HERDR_PLUGIN_CONFIG_DIR": pluginDir, // no config.toml here
		"HOME":                    home,
	})
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cloudflare.Mode != ModeQuick {
		t.Errorf("expected the home quick-mode config to load, got mode %q", cfg.Cloudflare.Mode)
	}
	if cfg.SourcePath != filepath.Join(homeCfgDir, "config.toml") {
		t.Errorf("SourcePath = %q, want the home config", cfg.SourcePath)
	}
}
