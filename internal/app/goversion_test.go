package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// supportedGoVersion is the exact Go patch this repository builds and tests with.
// It is pinned to a specific patch (not just a minor series) because that patch
// carries standard-library security fixes govulncheck flags against earlier 1.26
// patches. go.mod and mise.toml must pin this exact version; a drift fails the
// tests below so a toolchain bump is made everywhere at once.
const supportedGoVersion = "1.26.5"

// supportedGoSeries is the user-facing major.minor series ("Go 1.26"). CI
// workflows may pin the series rather than the exact patch: actions/setup-go
// resolves a bare series to the latest available patch, which is never older than
// supportedGoVersion, so the series pin still satisfies the security floor.
const supportedGoSeries = "1.26"

// goMinorSeries reduces a Go version token ("1.26", "1.26.5", "'1.26'",
// "go1.26.0") to its major.minor series ("1.26").
func goMinorSeries(v string) string {
	v = strings.Trim(strings.TrimSpace(v), "'\"")
	v = strings.TrimPrefix(v, "go")
	v = strings.TrimSpace(v)
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

func TestGoVersionMiseToml(t *testing.T) {
	t.Parallel()
	var cfg struct {
		Tools struct {
			Go string `toml:"go"`
		} `toml:"tools"`
	}
	if _, err := toml.DecodeFile(filepath.Join(repoRoot(t), "mise.toml"), &cfg); err != nil {
		t.Fatalf("decode mise.toml: %v", err)
	}
	// mise must pin the exact patch so a local `mise install` provisions the
	// security-fixed toolchain, not merely the latest 1.26.x.
	if got := strings.Trim(strings.TrimSpace(cfg.Tools.Go), "'\""); got != supportedGoVersion {
		t.Errorf("mise.toml [tools] go = %q, want exact %q", cfg.Tools.Go, supportedGoVersion)
	}
}

var goDirectiveRe = regexp.MustCompile(`(?m)^go\s+(\S+)`)

func TestGoVersionGoMod(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	m := goDirectiveRe.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("go.mod has no go directive")
	}
	// The go directive must be the exact patch so the module's minimum toolchain
	// is the security-fixed one.
	if got := m[1]; got != supportedGoVersion {
		t.Errorf("go.mod go directive = %q, want exact %q", got, supportedGoVersion)
	}
}

var goWorkflowVersionRe = regexp.MustCompile(`(?m)^\s*GO_VERSION:\s*['"]?([0-9][0-9.]*)['"]?`)

// TestGoVersionWorkflows enforces patch-aware agreement between the CI workflows'
// GO_VERSION and the exact toolchain pin. A workflow may declare either the exact
// patch or the major.minor series (which setup-go resolves to the latest patch,
// never older than the pin); any other value — a different patch or a different
// series — is drift and fails.
func TestGoVersionWorkflows(t *testing.T) {
	t.Parallel()
	for _, wf := range []string{"ci.yml", "release.yml"} {
		path := filepath.Join(repoRoot(t), ".github", "workflows", wf)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", wf, err)
		}
		m := goWorkflowVersionRe.FindStringSubmatch(string(data))
		if m == nil {
			t.Fatalf("%s declares no GO_VERSION", wf)
		}
		if err := goVersionCompatible(m[1]); err != nil {
			t.Errorf("%s GO_VERSION=%q: %v", wf, m[1], err)
		}
	}
}

// goVersionCompatible reports whether a declared Go version agrees with the exact
// pin. An exact patch must equal supportedGoVersion; a bare series must equal
// supportedGoSeries (setup-go then installs the latest patch of that series).
func goVersionCompatible(declared string) error {
	declared = strings.Trim(strings.TrimSpace(declared), "'\"")
	parts := strings.Split(declared, ".")
	switch len(parts) {
	case 3:
		if declared != supportedGoVersion {
			return errVersionMismatch("exact patch", declared, supportedGoVersion)
		}
	case 2:
		if goMinorSeries(declared) != supportedGoSeries {
			return errVersionMismatch("series", declared, supportedGoSeries)
		}
	default:
		return errVersionMismatch("series or exact patch", declared, supportedGoVersion)
	}
	return nil
}

func errVersionMismatch(kind, got, want string) error {
	return &versionMismatchError{kind: kind, got: got, want: want}
}

type versionMismatchError struct{ kind, got, want string }

func (e *versionMismatchError) Error() string {
	return "declares " + e.got + " but the pinned " + e.kind + " is " + e.want
}
