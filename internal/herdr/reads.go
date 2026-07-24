package herdr

import (
	"context"
	"fmt"
)

// ReadSource selects which slice of a pane's output to read. Wire values are
// snake_case; ParseReadSource accepts the hyphenated HTTP form too.
type ReadSource string

const (
	SourceVisible         ReadSource = "visible"
	SourceRecent          ReadSource = "recent"
	SourceRecentUnwrapped ReadSource = "recent_unwrapped"
	SourceDetection       ReadSource = "detection"
)

// ParseReadSource maps a user-facing source (accepting either underscore or
// hyphen for recent-unwrapped) to a wire ReadSource. An empty string defaults
// to visible.
func ParseReadSource(s string) (ReadSource, error) {
	switch s {
	case "", "visible":
		return SourceVisible, nil
	case "recent":
		return SourceRecent, nil
	case "recent_unwrapped", "recent-unwrapped":
		return SourceRecentUnwrapped, nil
	case "detection":
		return SourceDetection, nil
	default:
		return "", newError("invalid_params", "unknown read source: "+s)
	}
}

// ReadFormat selects text or ANSI rendering of pane output.
type ReadFormat string

const (
	FormatText ReadFormat = "text"
	FormatANSI ReadFormat = "ansi"
)

// ReadResult is the decoded pane/agent read payload.
type ReadResult struct {
	PaneID      string     `json:"pane_id"`
	WorkspaceID string     `json:"workspace_id"`
	TabID       string     `json:"tab_id"`
	Source      ReadSource `json:"source"`
	Format      ReadFormat `json:"format"`
	Text        string     `json:"text"`
	Revision    int64      `json:"revision"`
	Truncated   bool       `json:"truncated"`
}

type paneReadParams struct {
	PaneID    string     `json:"pane_id"`
	Source    ReadSource `json:"source"`
	Format    ReadFormat `json:"format,omitempty"`
	Lines     *int       `json:"lines,omitempty"`
	StripANSI bool       `json:"strip_ansi,omitempty"`
}

type paneReadResult struct {
	Type string     `json:"type"`
	Read ReadResult `json:"read"`
}

// PaneReadOptions configure a pane read. Lines <= 0 means the server default.
type PaneReadOptions struct {
	Source    ReadSource
	Format    ReadFormat
	Lines     int
	StripANSI bool
}

func linesPtr(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// PaneRead reads bounded output from a pane. The state engine and the HTTP read
// route use this; it never streams (the terminal bridge owns streaming).
func (c *Client) PaneRead(ctx context.Context, paneID string, opts PaneReadOptions) (*ReadResult, error) {
	if opts.Source == "" {
		opts.Source = SourceVisible
	}
	var res paneReadResult
	err := c.call(ctx, "pane.read", paneReadParams{
		PaneID:    paneID,
		Source:    opts.Source,
		Format:    opts.Format,
		Lines:     linesPtr(opts.Lines),
		StripANSI: opts.StripANSI,
	}, "pane_read", &res)
	if err != nil {
		return nil, err
	}
	return &res.Read, nil
}

type agentReadParams struct {
	Target    string     `json:"target"`
	Source    ReadSource `json:"source"`
	Format    ReadFormat `json:"format,omitempty"`
	Lines     *int       `json:"lines,omitempty"`
	StripANSI bool       `json:"strip_ansi,omitempty"`
}

// AgentRead reads bounded output through the resolved agent surface. target is
// a live agent name or the hosting pane id.
func (c *Client) AgentRead(ctx context.Context, target string, opts PaneReadOptions) (*ReadResult, error) {
	if opts.Source == "" {
		opts.Source = SourceVisible
	}
	var res paneReadResult
	err := c.call(ctx, "agent.read", agentReadParams{
		Target:    target,
		Source:    opts.Source,
		Format:    opts.Format,
		Lines:     linesPtr(opts.Lines),
		StripANSI: opts.StripANSI,
	}, "pane_read", &res)
	if err != nil {
		return nil, err
	}
	return &res.Read, nil
}

type agentListResult struct {
	Type   string  `json:"type"`
	Agents []Agent `json:"agents"`
}

// AgentList returns every live agent. The herd view sorts blocked-first at a
// higher layer.
func (c *Client) AgentList(ctx context.Context) ([]Agent, error) {
	var res agentListResult
	if err := c.call(ctx, "agent.list", struct{}{}, "agent_list", &res); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

type agentTarget struct {
	Target string `json:"target"`
}

type agentInfoResult struct {
	Type  string `json:"type"`
	Agent Agent  `json:"agent"`
}

// AgentGet resolves a single agent by live name or hosting pane id.
func (c *Client) AgentGet(ctx context.Context, target string) (*Agent, error) {
	var res agentInfoResult
	if err := c.call(ctx, "agent.get", agentTarget{Target: target}, "agent_info", &res); err != nil {
		return nil, err
	}
	return &res.Agent, nil
}

type workspaceTarget struct {
	WorkspaceID string `json:"workspace_id"`
}

type worktreeListResult struct {
	Type      string     `json:"type"`
	Source    string     `json:"source"`
	Worktrees []Worktree `json:"worktrees"`
}

// WorktreeList lists worktrees for a workspace. Herdr returns a structured
// error (for example not_git_worktree) when the workspace is not inside a git
// work tree; that error is preserved for the caller.
func (c *Client) WorktreeList(ctx context.Context, workspaceID string) ([]Worktree, error) {
	if workspaceID == "" {
		return nil, newError("invalid_params", "worktree list requires a workspace id")
	}
	var res worktreeListResult
	err := c.call(ctx, "worktree.list", workspaceTarget{WorkspaceID: workspaceID}, "worktree_list", &res)
	if err != nil {
		return nil, err
	}
	return res.Worktrees, nil
}

// String renders a ReadResult header for diagnostics without its body.
func (r ReadResult) String() string {
	return fmt.Sprintf("pane=%s rev=%d source=%s truncated=%t bytes=%d",
		r.PaneID, r.Revision, r.Source, r.Truncated, len(r.Text))
}
