package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretCommandTrimsOneNewline(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, argv []string) ([]byte, error) {
		return []byte("s3cr3t\n"), nil
	}
	got, err := ResolveSecretCommand(context.Background(), []string{"print"}, runner)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if string(got) != "s3cr3t" {
		t.Errorf("got %q, want %q", got, "s3cr3t")
	}
}

func TestResolveSecretCommandTrimsOnlyOneNewline(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ []string) ([]byte, error) { return []byte("a\n\n"), nil }
	got, _ := ResolveSecretCommand(context.Background(), []string{"print"}, runner)
	if string(got) != "a\n" {
		t.Errorf("got %q, want %q", got, "a\n")
	}
}

func TestResolveSecretCommandEmptyOutput(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ []string) ([]byte, error) { return []byte("\n"), nil }
	_, err := ResolveSecretCommand(context.Background(), []string{"print"}, runner)
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("expected no-output error, got %v", err)
	}
}

func TestResolveSecretCommandEmptyArgv(t *testing.T) {
	t.Parallel()
	_, err := ResolveSecretCommand(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty argv")
	}
}

func TestResolveSecretCommandErrorHidesOutput(t *testing.T) {
	t.Parallel()
	secret := "TOP-SECRET-VALUE"
	runner := func(_ context.Context, _ []string) ([]byte, error) {
		return []byte(secret), errors.New("boom")
	}
	_, err := ResolveSecretCommand(context.Background(), []string{"leaky"}, runner)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error message leaked secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "leaky") {
		t.Fatalf("error should name the program: %q", err.Error())
	}
}

// TestResolveSecretCommandDefaultRunner exercises the real, no-shell argv runner
// end to end using `cat` (present on macOS and Linux) against a temp file.
func TestResolveSecretCommandDefaultRunner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("real-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSecretCommand(context.Background(), []string{"cat", path}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if string(got) != "real-token" {
		t.Errorf("got %q, want %q", got, "real-token")
	}
}

func TestResolveSecretCommandOverflowFailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "big")
	big := make([]byte, maxSecretOutputBytes+1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveSecretCommand(context.Background(), []string{"cat", path}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected overflow error, got %v", err)
	}
}

func TestLimitedBufferOverflow(t *testing.T) {
	t.Parallel()
	var b limitedBuffer
	b.max = 4
	n, _ := b.Write([]byte("abcdef"))
	if n != 6 {
		t.Errorf("Write reported %d, want 6 (full write to keep child happy)", n)
	}
	if !b.overflow {
		t.Error("expected overflow flag set")
	}
	if string(b.Bytes()) != "abcd" {
		t.Errorf("buffered %q, want %q", b.Bytes(), "abcd")
	}
}
