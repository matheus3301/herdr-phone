package tunnel

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRunTokenCommand(t *testing.T) {
	t.Parallel()
	// Exactly one trailing newline is removed; the token body is preserved.
	tok, err := runTokenCommand(context.Background(), []string{"printf", "secret-token-123\n"})
	if err != nil {
		t.Fatalf("runTokenCommand: %v", err)
	}
	if tok != "secret-token-123" {
		t.Errorf("token = %q, want secret-token-123", tok)
	}
}

func TestRunTokenCommandPreservesInteriorAndSurroundingSpaces(t *testing.T) {
	t.Parallel()
	// A token with meaningful leading/trailing spaces must be preserved (only the
	// single trailing newline is trimmed), unlike the old TrimSpace behavior.
	tok, err := runTokenCommand(context.Background(), []string{"printf", "  spaced token  \n"})
	if err != nil {
		t.Fatalf("runTokenCommand: %v", err)
	}
	if tok != "  spaced token  " {
		t.Errorf("token = %q, want %q (surrounding spaces preserved)", tok, "  spaced token  ")
	}
}

func TestRunTokenCommandTrimsExactlyOneTrailingNewline(t *testing.T) {
	t.Parallel()
	// Two trailing newlines: only one is removed, leaving the other in the token.
	tok, err := runTokenCommand(context.Background(), []string{"printf", "tok\n\n"})
	if err != nil {
		t.Fatalf("runTokenCommand: %v", err)
	}
	if tok != "tok\n" {
		t.Errorf("token = %q, want %q (exactly one trailing newline trimmed)", tok, "tok\n")
	}

	// CRLF counts as one terminator.
	tokCRLF, err := runTokenCommand(context.Background(), []string{"printf", "tok\r\n"})
	if err != nil {
		t.Fatalf("runTokenCommand crlf: %v", err)
	}
	if tokCRLF != "tok" {
		t.Errorf("crlf token = %q, want %q", tokCRLF, "tok")
	}
}

func TestRunTokenCommandEmpty(t *testing.T) {
	t.Parallel()
	if _, err := runTokenCommand(context.Background(), []string{"printf", ""}); err == nil {
		t.Fatal("expected error for empty token output")
	}
	// A lone newline trims to empty -> no output.
	if _, err := runTokenCommand(context.Background(), []string{"printf", "\n"}); err == nil {
		t.Fatal("expected error for newline-only token output")
	}
	if _, err := runTokenCommand(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty argv")
	}
}

func TestRunTokenCommandOverflow(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	// Produce more than maxTokenBytes of output.
	script := "yes A | head -c 20000"
	_, err := runTokenCommand(context.Background(), []string{"sh", "-c", script})
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !errors.Is(err, errTokenTooLarge) {
		t.Fatalf("err = %v, want errors.Is errTokenTooLarge", err)
	}
}

func TestRunTokenCommandErrorNeverLeaksOutput(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	// A command that writes secret material to stdout then fails must not surface
	// that material in the returned error.
	_, err := runTokenCommand(context.Background(), []string{"sh", "-c", "printf SECRETLEAK; exit 3"})
	if err == nil {
		t.Fatal("expected error for failing token command")
	}
	if strings.Contains(err.Error(), "SECRETLEAK") {
		t.Fatalf("error leaked command output: %v", err)
	}
}

func TestWriteTokenFilePermsAndCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, cleanup, err := writeTokenFile(dir, "top-secret")
	if err != nil {
		t.Fatalf("writeTokenFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file perm = %o, want 0600", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "top-secret" {
		t.Errorf("token file content = %q", string(data))
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("token file %q not in dir %q", path, dir)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file should be removed, stat err = %v", err)
	}
	// cleanup is idempotent.
	if err := cleanup(); err != nil {
		t.Errorf("second cleanup: %v", err)
	}
}
