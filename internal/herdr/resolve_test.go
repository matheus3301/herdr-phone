package herdr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSocketPathPrecedence(t *testing.T) {
	// Not parallel: mutates process environment.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("HERDR_SOCKET_PATH", "/env/herdr.sock")
	got, err := ResolveSocketPath("/config/herdr.sock")
	if err != nil || got != "/config/herdr.sock" {
		t.Fatalf("configured should win: %q %v", got, err)
	}

	got, err = ResolveSocketPath("")
	if err != nil || got != "/env/herdr.sock" {
		t.Fatalf("env should be used when unconfigured: %q %v", got, err)
	}

	t.Setenv("HERDR_SOCKET_PATH", "")
	got, err = ResolveSocketPath("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "herdr", "herdr.sock")
	if got != want {
		t.Fatalf("default socket = %q want %q", got, want)
	}

	got, err = ResolveSocketPath("~/x/herdr.sock")
	if err != nil || got != filepath.Join(home, "x", "herdr.sock") {
		t.Fatalf("tilde expansion: %q %v", got, err)
	}
}

func TestResolveBinaryPathPrecedence(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "/env/herdr")
	if got := ResolveBinaryPath("/cfg/herdr"); got != "/cfg/herdr" {
		t.Fatalf("configured should win: %q", got)
	}
	if got := ResolveBinaryPath(""); got != "/env/herdr" {
		t.Fatalf("env should be used: %q", got)
	}
	t.Setenv("HERDR_BIN_PATH", "")
	if got := ResolveBinaryPath(""); got != "herdr" {
		t.Fatalf("default should be herdr on PATH: %q", got)
	}
}
