package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPathTilde(t *testing.T) {
	t.Parallel()
	got, err := ExpandPath("~/src", envMap(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean("/home/u/src") {
		t.Errorf("got %q", got)
	}
}

func TestExpandPathBareTilde(t *testing.T) {
	t.Parallel()
	got, err := ExpandPath("~", envMap(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/u" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPathEnvVar(t *testing.T) {
	t.Parallel()
	got, err := ExpandPath("$ROOT/x", envMap(map[string]string{"ROOT": "/data"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean("/data/x") {
		t.Errorf("got %q", got)
	}
}

func TestExpandPathUnsetVar(t *testing.T) {
	t.Parallel()
	_, err := ExpandPath("$MISSING/x", envMap(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("expected unset var error, got %v", err)
	}
}

func TestExpandPathTildeNoHome(t *testing.T) {
	t.Parallel()
	_, err := ExpandPath("~/x", envMap(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("expected HOME error, got %v", err)
	}
}

func TestExpandPathEmpty(t *testing.T) {
	t.Parallel()
	if _, err := ExpandPath("   ", envMap(map[string]string{})); err == nil {
		t.Fatal("expected empty-path error")
	}
}
