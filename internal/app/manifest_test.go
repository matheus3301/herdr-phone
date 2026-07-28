package app

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/matheus3301/herdr-phone/internal/buildinfo"
)

// repoRoot locates the repository root from this test file's location so the
// test is independent of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

type manifest struct {
	ID              string   `toml:"id"`
	Name            string   `toml:"name"`
	Version         string   `toml:"version"`
	MinHerdrVersion string   `toml:"min_herdr_version"`
	Platforms       []string `toml:"platforms"`
	Build           []struct {
		Command   []string `toml:"command"`
		Platforms []string `toml:"platforms"`
	} `toml:"build"`
	Actions []struct {
		ID       string   `toml:"id"`
		Title    string   `toml:"title"`
		Contexts []string `toml:"contexts"`
		Command  []string `toml:"command"`
	} `toml:"actions"`
	Panes []struct {
		ID string `toml:"id"`
	} `toml:"panes"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if _, err := toml.DecodeFile(filepath.Join(repoRoot(t), "herdr-plugin.toml"), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func TestManifestAgreement(t *testing.T) {
	t.Parallel()
	m := loadManifest(t)

	if m.ID != PluginID {
		t.Errorf("manifest id %q != app.PluginID %q", m.ID, PluginID)
	}
	if m.Name != buildinfo.DisplayName {
		t.Errorf("manifest name %q != buildinfo.DisplayName %q", m.Name, buildinfo.DisplayName)
	}
	if m.Version != buildinfo.Version {
		t.Errorf("manifest version %q != buildinfo.Version %q", m.Version, buildinfo.Version)
	}
	if m.MinHerdrVersion != buildinfo.MinHerdrVersion {
		t.Errorf("min_herdr_version %q != buildinfo.MinHerdrVersion %q", m.MinHerdrVersion, buildinfo.MinHerdrVersion)
	}
	if !reflect.DeepEqual(m.Platforms, []string{"macos"}) {
		t.Errorf("platforms = %v, want [macos]", m.Platforms)
	}
	if len(m.Build) != 1 || !reflect.DeepEqual(m.Build[0].Command, []string{"sh", "scripts/build.sh"}) {
		t.Errorf("build command = %+v", m.Build)
	}
	if len(m.Panes) != 0 {
		t.Errorf("expected no panes, got %d", len(m.Panes))
	}
}

func TestManifestActionsAgreement(t *testing.T) {
	t.Parallel()
	m := loadManifest(t)

	actions := map[string]struct {
		Contexts []string
		Command  []string
	}{}
	for _, a := range m.Actions {
		actions[a.ID] = struct {
			Contexts []string
			Command  []string
		}{a.Contexts, a.Command}
		if !reflect.DeepEqual(a.Contexts, []string{"global"}) {
			t.Errorf("action %q contexts = %v, want [global]", a.ID, a.Contexts)
		}
	}

	want := map[string][]string{
		ActionStart:      {"./bin/herdr-phone", "start"},
		ActionStartQuick: {"./bin/herdr-phone", "start", "--quick"},
		ActionStop:       {"./bin/herdr-phone", "stop"},
		ActionToggle:     {"./bin/herdr-phone", "toggle"},
		ActionStatus:     {"./bin/herdr-phone", "status"},
		ActionSetupLink:  {"./bin/herdr-phone", "setup-link"},
		ActionDoctor:     {"./bin/herdr-phone", "doctor"},
	}
	for id, cmd := range want {
		got, ok := actions[id]
		if !ok {
			t.Errorf("manifest missing action %q", id)
			continue
		}
		if !reflect.DeepEqual(got.Command, cmd) {
			t.Errorf("action %q command = %v, want %v", id, got.Command, cmd)
		}
	}
	if len(m.Actions) != len(want) {
		t.Errorf("manifest has %d actions, want %d", len(m.Actions), len(want))
	}
}

func TestManifestVersionIsSemantic(t *testing.T) {
	t.Parallel()
	// The release workflow requires the tag vX.Y.Z to equal v + manifest version.
	if got := buildinfo.Version; len(got) == 0 {
		t.Fatal("empty version")
	}
	m := loadManifest(t)
	parts := 0
	for _, r := range m.Version {
		if r == '.' {
			parts++
		}
	}
	if parts != 2 {
		t.Errorf("version %q is not semantic X.Y.Z", m.Version)
	}
}
