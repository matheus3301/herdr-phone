package herdr

// AgentStatus is one of Herdr's five lifecycle states. Any other value decodes
// as StatusUnknown-compatible text and must not be treated as completion.
type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// Active reports whether the status warrants the hot polling cadence.
func (s AgentStatus) Active() bool {
	return s == StatusWorking || s == StatusBlocked
}

// Snapshot is the decoded session.snapshot payload: the complete topology and
// agent bootstrap state. Unknown fields are tolerated and ignored.
//
// There is deliberately no top-level worktree array: `SessionSnapshot` in
// `herdr api schema --json` (protocol 17, schema 1) declares none, so a field
// for one would decode as empty forever and invite consumers to build on it.
// Worktree context comes from Workspace.Worktree; the full worktree inventory
// requires a separate `worktree.list` call.
type Snapshot struct {
	Version            string      `json:"version"`
	Protocol           int         `json:"protocol"`
	FocusedWorkspaceID string      `json:"focused_workspace_id"`
	FocusedTabID       string      `json:"focused_tab_id"`
	FocusedPaneID      string      `json:"focused_pane_id"`
	Workspaces         []Workspace `json:"workspaces"`
	Tabs               []Tab       `json:"tabs"`
	Panes              []Pane      `json:"panes"`
	Layouts            []Layout    `json:"layouts"`
	Agents             []Agent     `json:"agents"`
}

// WorkspaceWorktree is the git checkout provenance Herdr reports for a
// workspace. It is the authoritative source of a workspace's repository and
// checkout context: `session.snapshot` carries no top-level worktree array, so
// this is the only worktree context available without a separate
// `worktree.list` call.
type WorkspaceWorktree struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// Workspace is a top-level Space in the topology.
type Workspace struct {
	WorkspaceID string             `json:"workspace_id"`
	Number      int                `json:"number"`
	Label       string             `json:"label"`
	Focused     bool               `json:"focused"`
	PaneCount   int                `json:"pane_count"`
	TabCount    int                `json:"tab_count"`
	ActiveTabID string             `json:"active_tab_id"`
	AgentStatus AgentStatus        `json:"agent_status"`
	Worktree    *WorkspaceWorktree `json:"worktree,omitempty"`
}

// Tab is a terminal layout within a workspace.
type Tab struct {
	TabID       string      `json:"tab_id"`
	WorkspaceID string      `json:"workspace_id"`
	Number      int         `json:"number"`
	Label       string      `json:"label"`
	Focused     bool        `json:"focused"`
	PaneCount   int         `json:"pane_count"`
	AgentStatus AgentStatus `json:"agent_status"`
}

// Scroll describes a pane's scrollback position.
type Scroll struct {
	OffsetFromBottom    int `json:"offset_from_bottom"`
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

// AgentSession identifies the coding-agent session bound to a pane.
type AgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"` // "id" or "path"
	Value  string `json:"value"`
}

// Pane is a single terminal pane.
type Pane struct {
	PaneID                string        `json:"pane_id"`
	TerminalID            string        `json:"terminal_id"`
	WorkspaceID           string        `json:"workspace_id"`
	TabID                 string        `json:"tab_id"`
	Focused               bool          `json:"focused"`
	CWD                   string        `json:"cwd"`
	ForegroundCWD         string        `json:"foreground_cwd"`
	Agent                 string        `json:"agent,omitempty"`
	DisplayAgent          string        `json:"display_agent,omitempty"`
	AgentSession          *AgentSession `json:"agent_session,omitempty"`
	AgentStatus           AgentStatus   `json:"agent_status,omitempty"`
	Label                 string        `json:"label,omitempty"`
	Title                 string        `json:"title,omitempty"`
	TerminalTitle         string        `json:"terminal_title,omitempty"`
	TerminalTitleStripped string        `json:"terminal_title_stripped,omitempty"`
	Scroll                *Scroll       `json:"scroll,omitempty"`
	Revision              int64         `json:"revision"`
}

// Agent is a recognized coding agent occupying a pane.
type Agent struct {
	TerminalID             string        `json:"terminal_id"`
	Agent                  string        `json:"agent"`
	Name                   string        `json:"name,omitempty"`
	DisplayAgent           string        `json:"display_agent,omitempty"`
	Title                  string        `json:"title,omitempty"`
	AgentSession           *AgentSession `json:"agent_session,omitempty"`
	AgentStatus            AgentStatus   `json:"agent_status"`
	WorkspaceID            string        `json:"workspace_id"`
	TabID                  string        `json:"tab_id"`
	PaneID                 string        `json:"pane_id"`
	Focused                bool          `json:"focused"`
	InteractiveReady       bool          `json:"interactive_ready,omitempty"`
	LaunchPending          bool          `json:"launch_pending,omitempty"`
	ScreenDetectionSkipped bool          `json:"screen_detection_skipped,omitempty"`
	StateChangeSeq         int64         `json:"state_change_seq"`
	CWD                    string        `json:"cwd"`
	ForegroundCWD          string        `json:"foreground_cwd"`
	TerminalTitle          string        `json:"terminal_title,omitempty"`
	TerminalTitleStripped  string        `json:"terminal_title_stripped,omitempty"`
	Revision               int64         `json:"revision"`
}

// Rect is a terminal-cell rectangle.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LayoutPane places a pane within a tab layout.
type LayoutPane struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Rect    Rect   `json:"rect"`
}

// LayoutSplit describes one split node of a tab layout.
type LayoutSplit struct {
	ID        string  `json:"id"`
	Direction string  `json:"direction"`
	Ratio     float64 `json:"ratio"`
	Rect      Rect    `json:"rect"`
}

// Layout is the geometric arrangement of one tab.
type Layout struct {
	WorkspaceID   string        `json:"workspace_id"`
	TabID         string        `json:"tab_id"`
	Zoomed        bool          `json:"zoomed"`
	Area          Rect          `json:"area"`
	FocusedPaneID string        `json:"focused_pane_id"`
	Panes         []LayoutPane  `json:"panes"`
	Splits        []LayoutSplit `json:"splits"`
}

// Worktree is a git-backed worktree with checkout provenance.
type Worktree struct {
	Path             string `json:"path"`
	Label            string `json:"label"`
	Branch           string `json:"branch,omitempty"`
	IsBare           bool   `json:"is_bare"`
	IsDetached       bool   `json:"is_detached"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
	IsPrunable       bool   `json:"is_prunable"`
	OpenWorkspaceID  string `json:"open_workspace_id,omitempty"`
}

// OccupantFingerprint returns a stable key for the pane's current terminal
// occupant. The state engine bumps a pane's lifecycle generation when this
// changes. It combines the agent kind and its bound session identity, so a
// pane that swaps from one agent (or session) to another is detected even when
// the agent kind string is unchanged.
func (p Pane) OccupantFingerprint() string {
	fp := p.TerminalID + "\x00" + p.Agent
	if p.AgentSession != nil {
		fp += "\x00" + p.AgentSession.Source + "\x00" + p.AgentSession.Kind + "\x00" + p.AgentSession.Value
	}
	return fp
}
