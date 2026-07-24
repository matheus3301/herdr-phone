package herdr

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// CodeDiscovery marks a failure to discover startable agent kinds.
const CodeDiscovery = "agent_kinds_unavailable"

// DefaultMaxCommandBytes bounds each of a discovery subprocess's stdout and
// stderr streams.
const DefaultMaxCommandBytes = 64 << 10 // 64 KiB

// maxAgentKindLen bounds a single kind identifier.
const maxAgentKindLen = 64

// Runner executes a bounded, shell-free argv command. name is an executable
// path or PATH lookup — never a shell string — and args are literal arguments.
// It returns whatever was captured on stdout and stderr along with the process
// error (for example an *exec.ExitError for a nonzero exit); discovery inspects
// the output regardless of a nonzero exit.
type Runner interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr []byte, err error)
}

// ExecRunner is the production [Runner]. It launches the command directly with
// no shell, streams stdout/stderr into fixed-size buffers so a chatty or
// hostile binary cannot exhaust memory, and honors context cancellation by
// killing the process.
type ExecRunner struct {
	// MaxBytes bounds each captured stream; <= 0 uses [DefaultMaxCommandBytes].
	MaxBytes int
}

// Run implements [Runner].
func (r ExecRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxCommandBytes
	}
	cmd := exec.CommandContext(ctx, name, args...)
	// Explicitly no shell: CommandContext runs argv directly. Do not set a
	// shell interpreter or pass a single command string.
	var out, errb boundedBuffer
	out.max = maxBytes
	errb.max = maxBytes
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.bytes(), errb.bytes(), err
}

// boundedBuffer is an io.Writer that keeps at most max bytes and silently drops
// the rest, while still reporting a full write so the writer is never blocked
// or errored.
type boundedBuffer struct {
	max int
	buf []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - len(b.buf); room > 0 {
		n := min(room, len(p))
		b.buf = append(b.buf, p[:n]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) bytes() []byte { return b.buf }

// StartableAgentKinds discovers the agent kinds this Herdr build can start, by
// invoking the bare `herdr agent` command through the injected [Runner] and
// parsing its machine-readable "kinds:" line. Herdr prints that usage text (and
// the kinds line) to stderr and exits nonzero; discovery treats that expected
// usage exit as success and parses the output anyway. Kinds are returned in the
// binary's order, de-duplicated, with only valid identifiers kept.
//
// It never falls back to a compiled kind list: if no usable kinds line is
// found, it returns a [CodeDiscovery] error so callers surface a real problem
// rather than starting agents against a stale, hard-coded set.
func (c *Client) StartableAgentKinds(ctx context.Context) ([]string, error) {
	runner := c.runner
	if runner == nil {
		runner = ExecRunner{MaxBytes: DefaultMaxCommandBytes}
	}
	bin := c.bin
	if bin == "" {
		bin = ResolveBinaryPath("")
	}

	stdout, stderr, runErr := runner.Run(ctx, bin, []string{"agent"})

	// Cancellation and deadline must surface, not be masked by an empty parse.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, newError(CodeTimeout, "agent-kind discovery timed out")
		}
		return nil, newError(CodeCanceled, "agent-kind discovery canceled")
	}

	// The kinds line normally lands on stderr (usage output); accept stdout too.
	if kinds, ok := parseAgentKinds(stdout); ok {
		return kinds, nil
	}
	if kinds, ok := parseAgentKinds(stderr); ok {
		return kinds, nil
	}

	msg := "herdr agent output contained no parseable kinds: line"
	if runErr != nil {
		// A non-usage failure (binary missing, permission denied) explains the
		// empty output; include it without treating a mere nonzero exit as fatal.
		if _, isExit := errors.AsType[*exec.ExitError](runErr); !isExit {
			msg = "could not run herdr for agent-kind discovery: " + runErr.Error()
		}
	}
	return nil, newError(CodeDiscovery, msg)
}

// parseAgentKinds scans bounded command output for the first line whose trimmed
// text begins with "kinds:", then splits the remainder on "|". It preserves
// order, drops empty and invalid tokens, and de-duplicates. It reports ok=false
// when there is no kinds line or the line yields no valid identifier.
func parseAgentKinds(data []byte) ([]string, bool) {
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		rest, found := strings.CutPrefix(line, "kinds:")
		if !found {
			continue
		}
		var kinds []string
		seen := make(map[string]struct{})
		for field := range strings.SplitSeq(rest, "|") {
			kind := strings.TrimSpace(field)
			if kind == "" || !validKindIdent(kind) {
				continue
			}
			if _, dup := seen[kind]; dup {
				continue
			}
			seen[kind] = struct{}{}
			kinds = append(kinds, kind)
		}
		// A kinds line with no valid identifier is not a usable discovery.
		if len(kinds) == 0 {
			return nil, false
		}
		return kinds, true
	}
	return nil, false
}

// validKindIdent reports whether s is a valid agent-kind identifier:
// lowercase-letter start, then lowercase letters, digits, hyphen, or underscore.
func validKindIdent(s string) bool {
	if s == "" || len(s) > maxAgentKindLen {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
