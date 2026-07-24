package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// maxSecretOutputBytes bounds how much stdout a secret command may produce so a
// runaway command cannot exhaust memory. A truncated prefix would silently
// become a wrong secret, so overflow fails closed instead.
const maxSecretOutputBytes = 1 << 16 // 64 KiB

// SecretRunner runs an argv command with no shell and returns its stdout. It
// must never include the command's output in a returned error.
type SecretRunner func(ctx context.Context, argv []string) ([]byte, error)

// ResolveSecretCommand executes a configured secret command (an argv array, never
// a shell) and returns its output with a single trailing newline trimmed. The
// output is bounded and is never placed in an error message, so a failing command
// cannot leak partial secret material. The returned bytes are transient; callers
// must write them only to a mode 0600 file and never log them.
func ResolveSecretCommand(ctx context.Context, argv []string, runner SecretRunner) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("secret command is empty")
	}
	if runner == nil {
		runner = runSecretArgv
	}
	out, err := runner(ctx, argv)
	if err != nil {
		// Reference only the program name and the error, never the output.
		return nil, fmt.Errorf("secret command %q failed: %w", argv[0], err)
	}
	secret := trimOneTrailingNewline(out)
	if len(secret) == 0 {
		return nil, fmt.Errorf("secret command %q produced no output", argv[0])
	}
	return secret, nil
}

// runSecretArgv is the default SecretRunner. It executes argv directly, captures
// only bounded stdout, and discards stderr so a failing command cannot leak
// secret material through an error message.
func runSecretArgv(ctx context.Context, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout limitedBuffer
	stdout.max = maxSecretOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if stdout.overflow {
		return nil, fmt.Errorf("secret command output exceeded %d bytes", maxSecretOutputBytes)
	}
	return stdout.Bytes(), nil
}

// limitedBuffer captures up to max bytes and records whether more was written,
// reporting full writes so the child process is not disrupted.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - b.buf.Len()
	if remaining >= len(p) {
		b.buf.Write(p)
	} else {
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}
		b.overflow = true
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

// trimOneTrailingNewline removes a single trailing line terminator ("\r\n",
// "\n", or "\r") without touching other bytes that may belong to the secret.
func trimOneTrailingNewline(b []byte) []byte {
	s := string(b)
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return b[:len(b)-2]
	case strings.HasSuffix(s, "\n"), strings.HasSuffix(s, "\r"):
		return b[:len(b)-1]
	default:
		return b
	}
}
