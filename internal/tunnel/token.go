package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// maxTokenBytes bounds the accepted output of a token command. Cloudflare tunnel
// tokens are well under this; anything larger is treated as misconfiguration
// rather than being written to disk.
const maxTokenBytes = 8 * 1024

// errTokenTooLarge is returned when a token command produces more than
// maxTokenBytes of output.
var errTokenTooLarge = fmt.Errorf("tunnel: token command output exceeds %d bytes", maxTokenBytes)

// runTokenCommand executes the argv (no shell) and returns the token from its
// stdout. It delegates to config.ResolveSecretCommand so there is a single,
// spec-correct secret-resolution path (SPEC §8: output "trimmed once"): exactly
// one trailing "\r\n"/"\n"/"\r" is removed and any other leading/trailing
// whitespace that is part of the secret is preserved. Output is bounded to the
// tunnel's tighter limit via tunnelSecretRunner, and neither the command's
// output nor argv beyond the program name ever appears in an error. The token is
// only ever held in memory here and handed to writeTokenFile.
func runTokenCommand(ctx context.Context, argv []string) (string, error) {
	out, err := config.ResolveSecretCommand(ctx, argv, tunnelSecretRunner)
	if err != nil {
		return "", fmt.Errorf("tunnel: %w", err)
	}
	return string(out), nil
}

// tunnelSecretRunner is a config.SecretRunner that keeps the tunnel's tighter
// output bound (tunnel tokens are small) and guarantees the command's output
// never reaches an error. stderr is discarded so a chatty command cannot leak
// the secret or flood memory.
func tunnelSecretRunner(ctx context.Context, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout bytes.Buffer
	// Read at most maxTokenBytes+1 so we can detect overflow without buffering
	// unbounded data.
	cmd.Stdout = &limitedWriter{w: &stdout, limit: maxTokenBytes + 1}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, redactExecError(err)
	}
	if stdout.Len() > maxTokenBytes {
		return nil, errTokenTooLarge
	}
	return stdout.Bytes(), nil
}

// writeTokenFile writes token to a fresh temporary file with mode 0600 in dir
// (or the OS temp dir when dir is empty). It returns the path and a cleanup
// function that removes the file. cleanup is safe to call multiple times.
func writeTokenFile(dir, token string) (path string, cleanup func() error, err error) {
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "herdr-phone-cf-token-*")
	if err != nil {
		return "", nil, fmt.Errorf("tunnel: create token file: %w", err)
	}
	// os.CreateTemp already creates the file with mode 0600, but set it
	// explicitly so the guarantee does not depend on umask or platform.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("tunnel: chmod token file: %w", err)
	}
	if _, err := f.WriteString(token); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("tunnel: write token file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("tunnel: close token file: %w", err)
	}

	name := f.Name()
	var once bool
	cleanup = func() error {
		if once {
			return nil
		}
		once = true
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return name, cleanup, nil
}

// limitedWriter writes to w until limit bytes have been accepted, after which it
// silently discards further data. Length is tracked so the caller can detect an
// overflow.
type limitedWriter struct {
	w     io.Writer
	limit int
	n     int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	remaining := l.limit - l.n
	if remaining <= 0 {
		// Pretend to accept so the child is not blocked on a full pipe; the
		// caller detects the overflow via the recorded length.
		return len(p), nil
	}
	if len(p) > remaining {
		if _, err := l.w.Write(p[:remaining]); err != nil {
			return 0, err
		}
		l.n += remaining
		return len(p), nil
	}
	nn, err := l.w.Write(p)
	l.n += nn
	return nn, err
}
