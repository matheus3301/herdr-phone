package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Spec describes the terminal controller subprocess to launch.
type Spec struct {
	PaneID   string
	Cols     int
	Rows     int
	Takeover bool
}

// Controller is a running `herdr terminal session control` process abstracted
// for testing. The bridge reads NDJSON records from Stdout and writes NDJSON
// commands to Stdin.
type Controller interface {
	Stdout() io.Reader
	Stdin() io.Writer
	// Terminate asks the process (group) to stop.
	Terminate() error
	// Wait blocks until the process exits and returns its exit error, if any.
	Wait() error
}

// Launcher creates terminal controllers. Production uses ExecLauncher; tests
// inject a fake that returns scripted NDJSON without spawning a process.
type Launcher interface {
	Launch(ctx context.Context, spec Spec) (Controller, error)
}

// ExecLauncher launches the real Herdr controller subprocess.
//
// It follows section 7 of SPEC.md: no shell, exec.CommandContext for context
// cancellation, a nonzero WaitDelay so a wedged child cannot outlive the
// bridge, and its own process group so the whole group can be signaled.
type ExecLauncher struct {
	// BinPath is the resolved herdr binary (HERDR_BIN_PATH).
	BinPath string
	// SocketPath is the resolved Herdr socket (HERDR_SOCKET_PATH). It is passed
	// via the environment, never argv.
	SocketPath string
	// WaitDelay bounds how long the process may run after its context is
	// cancelled before it is force-killed. Zero uses a safe default.
	WaitDelay time.Duration
}

// Launch starts the controller subprocess for the given spec.
func (l ExecLauncher) Launch(ctx context.Context, spec Spec) (Controller, error) {
	if l.BinPath == "" {
		return nil, fmt.Errorf("terminal: empty herdr binary path")
	}
	args := []string{
		"terminal", "session", "control", spec.PaneID,
		"--cols", strconv.Itoa(spec.Cols),
		"--rows", strconv.Itoa(spec.Rows),
	}
	if spec.Takeover {
		args = append(args, "--takeover")
	}

	cmd := exec.CommandContext(ctx, l.BinPath, args...)
	cmd.Env = os.Environ()
	if l.SocketPath != "" {
		cmd.Env = append(cmd.Env, "HERDR_SOCKET_PATH="+l.SocketPath)
	}
	// Own process group so Terminate can signal the whole group, and set a
	// bounded WaitDelay so context cancellation cannot leak a wedged child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	waitDelay := l.WaitDelay
	if waitDelay <= 0 {
		waitDelay = 5 * time.Second
	}
	cmd.WaitDelay = waitDelay

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("terminal: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("terminal: stdout pipe: %w", err)
	}
	// Discard stderr rather than logging it: controller diagnostics can contain
	// terminal content, which must never be logged (section 13).
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("terminal: start controller: %w", err)
	}
	return &execController{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

type execController struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (c *execController) Stdout() io.Reader { return c.stdout }
func (c *execController) Stdin() io.Writer  { return c.stdin }

func (c *execController) Terminate() error {
	if c.cmd.Process == nil {
		return nil
	}
	// Signal the whole process group (negative pid). Ignore ESRCH-style errors;
	// the process may already be gone.
	pgid := c.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	// Closing stdin also nudges a well-behaved controller to exit.
	_ = c.stdin.Close()
	return nil
}

func (c *execController) Wait() error {
	return c.cmd.Wait()
}
