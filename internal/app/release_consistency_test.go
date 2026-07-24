package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestGoreleaserArchiveTemplateMatchesBuildScript pins the GoReleaser archive
// name template and the install script's download name to the same shape, so a
// release archive is always downloadable by scripts/build.sh's no-toolchain
// fallback. A drift in either would silently break offline installs.
func TestGoreleaserArchiveTemplateMatchesBuildScript(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	gr := repoFileText(t, filepath.Join(root, ".goreleaser.yml"))
	const wantTemplate = `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`
	if !strings.Contains(gr, wantTemplate) {
		t.Errorf(".goreleaser.yml archive name_template must be %q", wantTemplate)
	}
	if !strings.Contains(gr, "checksums.txt") {
		t.Error(".goreleaser.yml must publish checksums.txt (build.sh verifies it)")
	}

	build := repoFileText(t, filepath.Join(root, "scripts", "build.sh"))
	// The download name must expand to <project>_<version>_<os>_<arch>.tar.gz,
	// matching the GoReleaser template above.
	const wantArchive = `${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}.tar.gz`
	if !strings.Contains(build, wantArchive) {
		t.Errorf("scripts/build.sh must download an archive named %q to match the GoReleaser template", wantArchive)
	}
	if !strings.Contains(build, "checksums.txt") {
		t.Error("scripts/build.sh must verify checksums.txt")
	}
}

// supportedNodeVersion is the exact Node patch mise provisions locally. It is
// pinned (not just the major) because a bare "22" let mise reuse an
// already-installed 22.3.0, below Vite 7's Node 22.12+ floor. Its source is
// `mise latest node@22`.
const supportedNodeVersion = "22.23.1"

// supportedNodeMajor is the user-facing Node major ("Node 22"). CI workflows may
// pin the major; actions/setup-node resolves it to the latest patch, never older
// than supportedNodeVersion.
const supportedNodeMajor = "22"

// TestNodeVersionConsistency requires mise to pin the exact Node patch and every
// CI workflow's NODE_VERSION to agree with it patch-aware (the exact patch, a
// major.minor prefix of it, or the bare "22" major that setup-node resolves to
// the latest patch) — not merely the same major.
func TestNodeVersionConsistency(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	var mise struct {
		Tools struct {
			Node string `toml:"node"`
		} `toml:"tools"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "mise.toml"), &mise); err != nil {
		t.Fatalf("decode mise.toml: %v", err)
	}
	// mise must pin the exact patch so a local `mise install` provisions a
	// Vite-supported Node, not merely the latest already-installed 22.x.
	if got := strings.Trim(strings.TrimSpace(mise.Tools.Node), "'\""); got != supportedNodeVersion {
		t.Errorf("mise.toml [tools] node = %q, want exact %q", mise.Tools.Node, supportedNodeVersion)
	}

	for _, wf := range []string{"ci.yml", "release.yml"} {
		content := repoFileText(t, filepath.Join(root, ".github", "workflows", wf))
		declared, ok := workflowNodeVersion(content)
		if !ok {
			t.Errorf("%s declares no NODE_VERSION", wf)
			continue
		}
		if err := nodeVersionCompatible(declared); err != nil {
			t.Errorf("%s NODE_VERSION=%q: %v", wf, declared, err)
		}
	}
}

var nodeVersionRe = regexp.MustCompile(`(?m)^\s*NODE_VERSION:\s*['"]?([0-9][0-9.]*)['"]?`)

func workflowNodeVersion(content string) (string, bool) {
	m := nodeVersionRe.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// nodeVersionCompatible reports whether a declared Node version agrees with the
// exact pin: the bare major ("22", resolved by setup-node to the latest patch),
// a "major.minor" prefix of the pin, or the exact patch. Any other value — a
// different major or a different exact patch — is drift.
func nodeVersionCompatible(declared string) error {
	declared = strings.Trim(strings.TrimSpace(declared), "'\"")
	switch parts := strings.Split(declared, "."); len(parts) {
	case 1:
		if declared != supportedNodeMajor {
			return &versionMismatchError{kind: "major", got: declared, want: supportedNodeMajor}
		}
	case 2:
		if !strings.HasPrefix(supportedNodeVersion, declared+".") {
			return &versionMismatchError{kind: "major.minor prefix of", got: declared, want: supportedNodeVersion}
		}
	case 3:
		if declared != supportedNodeVersion {
			return &versionMismatchError{kind: "exact patch", got: declared, want: supportedNodeVersion}
		}
	default:
		return &versionMismatchError{kind: "major or exact patch", got: declared, want: supportedNodeVersion}
	}
	return nil
}

// repoFileText reads a repo file as text, failing the test on error.
func repoFileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
