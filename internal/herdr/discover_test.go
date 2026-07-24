package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner is an injected Runner that records its invocation and returns
// scripted output. It optionally blocks until ctx is done (cancellation test).
type fakeRunner struct {
	stdout, stderr []byte
	err            error
	block          bool

	mu       sync.Mutex
	gotName  string
	gotArgs  []string
	runCalls int
}

func (r *fakeRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	r.mu.Lock()
	r.gotName = name
	r.gotArgs = append([]string(nil), args...)
	r.runCalls++
	r.mu.Unlock()
	if r.block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return r.stdout, r.stderr, r.err
}

// usageExit fabricates the nonzero usage exit real Herdr returns.
func usageExit() error {
	// A real *exec.ExitError requires a completed process; emulate one cheaply.
	cmd := exec.Command("false")
	return cmd.Run() // returns *exec.ExitError with code 1
}

const realKindsLine = "  kinds: pi|claude|codex|gemini|cursor|devin|agy|cline|omp|mastracode|opencode|copilot|kimi|kiro|droid|amp|grok|hermes|kilo|qodercli|maki\n"

func clientWith(r Runner) *Client {
	return NewClient(nil, WithRunner(r), WithBin("/opt/bin/herdr"))
}

func TestDiscoverKindsFromStderrOnUsageExit(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{
		stderr: []byte("herdr agent commands:\n  herdr agent list\n" + realKindsLine),
		err:    usageExit(),
	}
	c := clientWith(r)
	kinds, err := c.StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	want := []string{"pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline", "omp",
		"mastracode", "opencode", "copilot", "kimi", "kiro", "droid", "amp", "grok", "hermes", "kilo", "qodercli", "maki"}
	if !equalStrings(kinds, want) {
		t.Fatalf("kinds mismatch:\n got %v\nwant %v", kinds, want)
	}
	// Invocation must be the bare `herdr agent`, no shell, no extra args.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gotName != "/opt/bin/herdr" {
		t.Fatalf("ran %q, want the injected binary", r.gotName)
	}
	if len(r.gotArgs) != 1 || r.gotArgs[0] != "agent" {
		t.Fatalf("args = %v, want [agent]", r.gotArgs)
	}
}

func TestDiscoverKindsFromStdout(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{stdout: []byte(realKindsLine)}
	kinds, err := clientWith(r).StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 21 || kinds[0] != "pi" {
		t.Fatalf("unexpected kinds: %v", kinds)
	}
}

func TestDiscoverStdoutPreferredOverStderr(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{
		stdout: []byte("kinds: alpha|beta\n"),
		stderr: []byte("kinds: gamma\n"),
	}
	kinds, err := clientWith(r).StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(kinds, []string{"alpha", "beta"}) {
		t.Fatalf("stdout should win: %v", kinds)
	}
}

func TestDiscoverPreservesOrderAndDeduplicates(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{stderr: []byte("kinds: codex|claude|codex|gemini|claude\n")}
	kinds, err := clientWith(r).StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(kinds, []string{"codex", "claude", "gemini"}) {
		t.Fatalf("order/dedup wrong: %v", kinds)
	}
}

func TestDiscoverDropsMalformedKeepsValid(t *testing.T) {
	t.Parallel()
	// Uppercase, spaces, leading digit/symbol, and empty fields are dropped.
	r := &fakeRunner{stderr: []byte("kinds: claude|CODEX|co dex||1bad|-x|opencode|\n")}
	kinds, err := clientWith(r).StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(kinds, []string{"claude", "opencode"}) {
		t.Fatalf("malformed not filtered: %v", kinds)
	}
}

func TestDiscoverErrorsWhenNoKindsLine(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{stderr: []byte("herdr agent commands:\n  herdr agent list\n"), err: usageExit()}
	_, err := clientWith(r).StartableAgentKinds(context.Background())
	if !IsCode(err, CodeDiscovery) {
		t.Fatalf("want %s, got %v", CodeDiscovery, err)
	}
}

func TestDiscoverErrorsWhenKindsLineEmptyOrAllInvalid(t *testing.T) {
	t.Parallel()
	for name, out := range map[string]string{
		"empty":       "kinds:\n",
		"only-seps":   "kinds: |||\n",
		"all-invalid": "kinds: BAD|1x|a b\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := &fakeRunner{stderr: []byte(out)}
			_, err := clientWith(r).StartableAgentKinds(context.Background())
			if !IsCode(err, CodeDiscovery) {
				t.Fatalf("want %s (never fall back to a compiled list), got %v", CodeDiscovery, err)
			}
		})
	}
}

func TestDiscoverNeverFallsBackWhenBinaryMissing(t *testing.T) {
	t.Parallel()
	// A start failure (not a usage exit) with no output must be an error, not a
	// hard-coded kind list.
	r := &fakeRunner{err: errors.New("exec: \"herdr\": executable file not found in $PATH")}
	_, err := clientWith(r).StartableAgentKinds(context.Background())
	if !IsCode(err, CodeDiscovery) {
		t.Fatalf("want %s, got %v", CodeDiscovery, err)
	}
	if !strings.Contains(err.Error(), "could not run herdr") {
		t.Fatalf("error should explain the run failure: %v", err)
	}
}

func TestDiscoverCancellation(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{block: true}
	c := clientWith(r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.StartableAgentKinds(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !IsCode(err, CodeCanceled) {
			t.Fatalf("want canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("discovery did not return after cancel")
	}
}

func TestDiscoverDeadline(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{block: true}
	c := clientWith(r)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.StartableAgentKinds(ctx)
	if !IsCode(err, CodeTimeout) {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestDiscoverResolvesBinaryLazily(t *testing.T) {
	// Not parallel: sets process env.
	t.Setenv("HERDR_BIN_PATH", "/env/herdr")
	r := &fakeRunner{stderr: []byte(realKindsLine)}
	// No WithBin: bin resolves from env.
	c := NewClient(nil, WithRunner(r))
	if _, err := c.StartableAgentKinds(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gotName != "/env/herdr" {
		t.Fatalf("bin not resolved from env: %q", r.gotName)
	}
}

// --- ExecRunner tests (real subprocess, still hermetic) ----------------------

func TestExecRunnerBoundsOutput(t *testing.T) {
	t.Parallel()
	// Emit far more than the cap; the runner must truncate.
	r := ExecRunner{MaxBytes: 128}
	script := "for i in $(seq 1 100000); do printf 'x'; done"
	out, _, _ := r.Run(context.Background(), "/bin/sh", []string{"-c", script})
	if len(out) != 128 {
		t.Fatalf("stdout not bounded: got %d bytes want 128", len(out))
	}
}

func TestExecRunnerNoShellInterpretation(t *testing.T) {
	t.Parallel()
	// The argument contains shell metacharacters. With no shell, echo prints it
	// verbatim; a shell would expand $(...) and split on ';'.
	danger := "hello; echo PWNED $(whoami) `id`"
	r := ExecRunner{MaxBytes: 4096}
	out, _, err := r.Run(context.Background(), "/bin/echo", []string{danger})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != danger {
		t.Fatalf("argv was shell-interpreted:\n got %q\nwant %q", got, danger)
	}
	if strings.Contains(got, "PWNED\n") {
		t.Fatal("command substitution executed")
	}
}

func TestExecRunnerCapturesStderrAndExitError(t *testing.T) {
	t.Parallel()
	r := ExecRunner{}
	_, stderr, err := r.Run(context.Background(), "/bin/sh", []string{"-c", "echo oops 1>&2; exit 3"})
	if strings.TrimSpace(string(stderr)) != "oops" {
		t.Fatalf("stderr not captured: %q", stderr)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exec.ExitError, got %v", err)
	}
}

func TestExecRunnerCancelKillsProcess(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := ExecRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := r.Run(ctx, "/bin/sh", []string{"-c", "sleep 10"})
	if err == nil {
		t.Fatal("expected an error from a killed process")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("process was not killed promptly on context expiry")
	}
}

// --- end-to-end against the real installed binary, if present ---------------

func TestDiscoverAgainstRealHerdrIfPresent(t *testing.T) {
	t.Parallel()
	path, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr not on PATH")
	}
	c := NewClient(nil, WithBin(path))
	kinds, err := c.StartableAgentKinds(context.Background())
	if err != nil {
		t.Fatalf("live discovery failed: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("live discovery returned no kinds")
	}
	for _, k := range kinds {
		if !validKindIdent(k) {
			t.Fatalf("live kind %q is not a valid identifier", k)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
