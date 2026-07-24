package herdr

import "context"

// This file is the complete, closed allowlist of Herdr mutations the relay may
// perform. There is no generic "invoke method" entry point: a browser request
// can only ever map to one of these typed functions with explicit ids. The
// excluded methods from the spec (server.stop, live handoff, plugin/integration
// administration, arbitrary server launch) have no function here at all.

// ---- Workspaces --------------------------------------------------------------

// WorkspaceCreated is the result of creating a workspace: the new workspace and
// its automatically created first tab and root pane.
type WorkspaceCreated struct {
	Type      string    `json:"type"`
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

type workspaceCreateParams struct {
	CWD   *string           `json:"cwd,omitempty"`
	Label *string           `json:"label,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
	Focus bool              `json:"focus"`
}

// WorkspaceCreateOptions configure workspace creation.
type WorkspaceCreateOptions struct {
	CWD   string
	Label string
	Env   map[string]string
	Focus bool
}

// WorkspaceCreate creates a workspace (and its first tab and root pane).
func (c *Client) WorkspaceCreate(ctx context.Context, opts WorkspaceCreateOptions) (*WorkspaceCreated, error) {
	var res WorkspaceCreated
	err := c.call(ctx, "workspace.create", workspaceCreateParams{
		CWD:   optStr(opts.CWD),
		Label: optStr(opts.Label),
		Env:   opts.Env,
		Focus: opts.Focus,
	}, "workspace_created", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

type workspaceInfoResult struct {
	Type      string    `json:"type"`
	Workspace Workspace `json:"workspace"`
}

// WorkspaceFocus focuses a workspace by explicit id.
func (c *Client) WorkspaceFocus(ctx context.Context, workspaceID string) (*Workspace, error) {
	return c.workspaceByID(ctx, "workspace.focus", workspaceID, workspaceTarget{WorkspaceID: workspaceID})
}

type workspaceRenameParams struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// WorkspaceRename relabels a workspace.
func (c *Client) WorkspaceRename(ctx context.Context, workspaceID, label string) (*Workspace, error) {
	return c.workspaceByID(ctx, "workspace.rename", workspaceID,
		workspaceRenameParams{WorkspaceID: workspaceID, Label: label})
}

func (c *Client) workspaceByID(ctx context.Context, method, workspaceID string, params any) (*Workspace, error) {
	if workspaceID == "" {
		return nil, newError("invalid_params", method+" requires a workspace id")
	}
	var res workspaceInfoResult
	if err := c.call(ctx, method, params, "workspace_info", &res); err != nil {
		return nil, err
	}
	return &res.Workspace, nil
}

// WorkspaceClose closes a workspace. Callers must gate this behind a
// confirmation nonce at the HTTP layer.
func (c *Client) WorkspaceClose(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return newError("invalid_params", "workspace.close requires a workspace id")
	}
	return c.call(ctx, "workspace.close", workspaceTarget{WorkspaceID: workspaceID}, "ok", nil)
}

// ---- Tabs --------------------------------------------------------------------

// TabCreated is the result of creating a tab.
type TabCreated struct {
	Type     string `json:"type"`
	Tab      Tab    `json:"tab"`
	RootPane Pane   `json:"root_pane"`
}

type tabCreateParams struct {
	WorkspaceID *string           `json:"workspace_id,omitempty"`
	CWD         *string           `json:"cwd,omitempty"`
	Label       *string           `json:"label,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Focus       bool              `json:"focus"`
}

// TabCreateOptions configure tab creation.
type TabCreateOptions struct {
	WorkspaceID string
	CWD         string
	Label       string
	Env         map[string]string
	Focus       bool
}

// TabCreate creates a tab in a workspace (or the focused workspace when
// WorkspaceID is empty).
func (c *Client) TabCreate(ctx context.Context, opts TabCreateOptions) (*TabCreated, error) {
	var res TabCreated
	err := c.call(ctx, "tab.create", tabCreateParams{
		WorkspaceID: optStr(opts.WorkspaceID),
		CWD:         optStr(opts.CWD),
		Label:       optStr(opts.Label),
		Env:         opts.Env,
		Focus:       opts.Focus,
	}, "tab_created", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

type tabTarget struct {
	TabID string `json:"tab_id"`
}

type tabInfoResult struct {
	Type string `json:"type"`
	Tab  Tab    `json:"tab"`
}

// TabFocus focuses a tab by explicit id.
func (c *Client) TabFocus(ctx context.Context, tabID string) (*Tab, error) {
	return c.tabByID(ctx, "tab.focus", tabID, tabTarget{TabID: tabID})
}

type tabRenameParams struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

// TabRename relabels a tab.
func (c *Client) TabRename(ctx context.Context, tabID, label string) (*Tab, error) {
	return c.tabByID(ctx, "tab.rename", tabID, tabRenameParams{TabID: tabID, Label: label})
}

func (c *Client) tabByID(ctx context.Context, method, tabID string, params any) (*Tab, error) {
	if tabID == "" {
		return nil, newError("invalid_params", method+" requires a tab id")
	}
	var res tabInfoResult
	if err := c.call(ctx, method, params, "tab_info", &res); err != nil {
		return nil, err
	}
	return &res.Tab, nil
}

type tabMoveParams struct {
	TabID       string `json:"tab_id"`
	InsertIndex int    `json:"insert_index"`
}

// TabMove reorders a tab to insertIndex within its workspace and returns the
// resulting authoritative tab order.
func (c *Client) TabMove(ctx context.Context, tabID string, insertIndex int) ([]Tab, error) {
	if tabID == "" {
		return nil, newError("invalid_params", "tab.move requires a tab id")
	}
	if insertIndex < 0 {
		return nil, newError("invalid_params", "tab.move insert_index must be non-negative")
	}
	var res struct {
		Type string `json:"type"`
		Tabs []Tab  `json:"tabs"`
	}
	err := c.call(ctx, "tab.move", tabMoveParams{TabID: tabID, InsertIndex: insertIndex}, "tab_list", &res)
	if err != nil {
		return nil, err
	}
	return res.Tabs, nil
}

// TabClose closes a tab. Gate behind a confirmation nonce at the HTTP layer.
func (c *Client) TabClose(ctx context.Context, tabID string) error {
	if tabID == "" {
		return newError("invalid_params", "tab.close requires a tab id")
	}
	return c.call(ctx, "tab.close", tabTarget{TabID: tabID}, "ok", nil)
}

// ---- Panes -------------------------------------------------------------------

// SplitDirection is a pane split axis.
type SplitDirection string

const (
	SplitRight SplitDirection = "right"
	SplitDown  SplitDirection = "down"
)

func (d SplitDirection) valid() bool { return d == SplitRight || d == SplitDown }

// PaneDirection is a spatial direction for resize/swap/focus.
type PaneDirection string

const (
	DirLeft  PaneDirection = "left"
	DirRight PaneDirection = "right"
	DirUp    PaneDirection = "up"
	DirDown  PaneDirection = "down"
)

func (d PaneDirection) valid() bool {
	switch d {
	case DirLeft, DirRight, DirUp, DirDown:
		return true
	}
	return false
}

type paneSplitParams struct {
	TargetPaneID *string           `json:"target_pane_id,omitempty"`
	WorkspaceID  *string           `json:"workspace_id,omitempty"`
	Direction    SplitDirection    `json:"direction"`
	Ratio        *float64          `json:"ratio,omitempty"`
	CWD          *string           `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Focus        bool              `json:"focus"`
}

// PaneSplitOptions configure a split. TargetPaneID is the pane being split and
// is required by the relay to avoid relying on UI focus.
type PaneSplitOptions struct {
	TargetPaneID string
	Direction    SplitDirection
	Ratio        float64 // 0 means server default
	CWD          string
	Env          map[string]string
	Focus        bool
}

type paneInfoResult struct {
	Type string `json:"type"`
	Pane Pane   `json:"pane"`
}

// PaneSplit splits a pane right or down and returns the new pane.
func (c *Client) PaneSplit(ctx context.Context, opts PaneSplitOptions) (*Pane, error) {
	if opts.TargetPaneID == "" {
		return nil, newError("invalid_params", "pane.split requires an explicit target pane id")
	}
	if !opts.Direction.valid() {
		return nil, newError("invalid_params", "pane.split direction must be right or down")
	}
	var ratio *float64
	if opts.Ratio > 0 {
		r := opts.Ratio
		ratio = &r
	}
	var res paneInfoResult
	err := c.call(ctx, "pane.split", paneSplitParams{
		TargetPaneID: &opts.TargetPaneID,
		Direction:    opts.Direction,
		Ratio:        ratio,
		CWD:          optStr(opts.CWD),
		Env:          opts.Env,
		Focus:        opts.Focus,
	}, "pane_info", &res)
	if err != nil {
		return nil, err
	}
	return &res.Pane, nil
}

type paneTarget struct {
	PaneID string `json:"pane_id"`
}

// PaneFocus focuses a pane by explicit id.
func (c *Client) PaneFocus(ctx context.Context, paneID string) (*Pane, error) {
	return c.paneByID(ctx, "pane.focus", paneTarget{PaneID: paneID}, paneID)
}

type paneRenameParams struct {
	PaneID string  `json:"pane_id"`
	Label  *string `json:"label"`
}

// PaneRename sets or clears a pane label. An empty label clears it.
func (c *Client) PaneRename(ctx context.Context, paneID, label string) (*Pane, error) {
	return c.paneByID(ctx, "pane.rename", paneRenameParams{PaneID: paneID, Label: optStr(label)}, paneID)
}

func (c *Client) paneByID(ctx context.Context, method string, params any, paneID string) (*Pane, error) {
	if paneID == "" {
		return nil, newError("invalid_params", method+" requires a pane id")
	}
	var res paneInfoResult
	if err := c.call(ctx, method, params, "pane_info", &res); err != nil {
		return nil, err
	}
	return &res.Pane, nil
}

type paneResizeParams struct {
	PaneID    string        `json:"pane_id"`
	Direction PaneDirection `json:"direction"`
	Amount    *float64      `json:"amount,omitempty"`
}

// PaneResizeResult is the pane.resize payload.
type PaneResizeResult struct {
	Type   string `json:"type"`
	Resize struct {
		Changed       bool    `json:"changed"`
		PaneID        string  `json:"pane_id"`
		FocusedPaneID string  `json:"focused_pane_id"`
		Reason        *string `json:"reason,omitempty"`
		Layout        *Layout `json:"layout,omitempty"`
	} `json:"resize"`
}

// PaneResize resizes a pane toward a direction. Amount <= 0 uses the server
// default step.
func (c *Client) PaneResize(ctx context.Context, paneID string, dir PaneDirection, amount float64) (*PaneResizeResult, error) {
	if paneID == "" {
		return nil, newError("invalid_params", "pane.resize requires a pane id")
	}
	if !dir.valid() {
		return nil, newError("invalid_params", "pane.resize direction is invalid")
	}
	var amt *float64
	if amount > 0 {
		amt = &amount
	}
	var res PaneResizeResult
	err := c.call(ctx, "pane.resize", paneResizeParams{PaneID: paneID, Direction: dir, Amount: amt}, "pane_resize", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// ZoomMode selects zoom behavior.
type ZoomMode string

const (
	ZoomToggle ZoomMode = "toggle"
	ZoomOn     ZoomMode = "on"
	ZoomOff    ZoomMode = "off"
)

func (z ZoomMode) valid() bool { return z == ZoomToggle || z == ZoomOn || z == ZoomOff }

type paneZoomParams struct {
	PaneID string   `json:"pane_id"`
	Mode   ZoomMode `json:"mode"`
}

// PaneZoomResult is the pane.zoom payload.
type PaneZoomResult struct {
	Type string `json:"type"`
	Zoom struct {
		Changed       bool   `json:"changed"`
		ZoomChanged   bool   `json:"zoom_changed"`
		FocusChanged  bool   `json:"focus_changed"`
		Zoomed        bool   `json:"zoomed"`
		PaneID        string `json:"pane_id"`
		FocusedPaneID string `json:"focused_pane_id"`
	} `json:"zoom"`
}

// PaneZoom toggles or sets pane zoom.
func (c *Client) PaneZoom(ctx context.Context, paneID string, mode ZoomMode) (*PaneZoomResult, error) {
	if paneID == "" {
		return nil, newError("invalid_params", "pane.zoom requires a pane id")
	}
	if !mode.valid() {
		return nil, newError("invalid_params", "pane.zoom mode is invalid")
	}
	var res PaneZoomResult
	if err := c.call(ctx, "pane.zoom", paneZoomParams{PaneID: paneID, Mode: mode}, "pane_zoom", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type paneSwapParams struct {
	SourcePaneID string `json:"source_pane_id"`
	TargetPaneID string `json:"target_pane_id"`
}

// PaneSwapResult is the pane.swap payload.
type PaneSwapResult struct {
	Type string `json:"type"`
	Swap struct {
		Changed       bool   `json:"changed"`
		SourcePaneID  string `json:"source_pane_id"`
		TargetPaneID  string `json:"target_pane_id"`
		FocusedPaneID string `json:"focused_pane_id"`
	} `json:"swap"`
}

// PaneSwap swaps two panes by explicit ids.
func (c *Client) PaneSwap(ctx context.Context, sourcePaneID, targetPaneID string) (*PaneSwapResult, error) {
	if sourcePaneID == "" || targetPaneID == "" {
		return nil, newError("invalid_params", "pane.swap requires source and target pane ids")
	}
	var res PaneSwapResult
	err := c.call(ctx, "pane.swap",
		paneSwapParams{SourcePaneID: sourcePaneID, TargetPaneID: targetPaneID}, "pane_swap", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// PaneMoveDestination is a typed, closed set of move targets. Exactly one
// constructor ([MoveToTab], [MoveToNewTab], [MoveToNewWorkspace]) builds a
// valid value.
type PaneMoveDestination struct {
	kind        string
	tabID       string
	split       SplitDirection
	targetPane  string
	ratio       *float64
	label       string
	tabLabel    string
	workspaceID string
}

// MoveToTab moves a pane into an existing tab, split from an optional target
// pane.
func MoveToTab(tabID string, split SplitDirection, targetPaneID string, ratio float64) PaneMoveDestination {
	d := PaneMoveDestination{kind: "tab", tabID: tabID, split: split, targetPane: targetPaneID}
	if ratio > 0 {
		d.ratio = &ratio
	}
	return d
}

// MoveToNewTab moves a pane into a new tab, optionally in another workspace.
func MoveToNewTab(workspaceID, label string) PaneMoveDestination {
	return PaneMoveDestination{kind: "new_tab", workspaceID: workspaceID, label: label}
}

// MoveToNewWorkspace moves a pane into a brand-new workspace.
func MoveToNewWorkspace(label, tabLabel string) PaneMoveDestination {
	return PaneMoveDestination{kind: "new_workspace", label: label, tabLabel: tabLabel}
}

func (d PaneMoveDestination) wire() (map[string]any, error) {
	m := map[string]any{}
	switch d.kind {
	case "tab":
		if d.tabID == "" {
			return nil, newError("invalid_params", "move to tab requires a tab id")
		}
		if !d.split.valid() {
			return nil, newError("invalid_params", "move to tab requires split right or down")
		}
		m["type"] = "tab"
		m["tab_id"] = d.tabID
		m["split"] = string(d.split)
		if d.targetPane != "" {
			m["target_pane_id"] = d.targetPane
		}
		if d.ratio != nil {
			m["ratio"] = *d.ratio
		}
	case "new_tab":
		m["type"] = "new_tab"
		if d.workspaceID != "" {
			m["workspace_id"] = d.workspaceID
		}
		if d.label != "" {
			m["label"] = d.label
		}
	case "new_workspace":
		m["type"] = "new_workspace"
		if d.label != "" {
			m["label"] = d.label
		}
		if d.tabLabel != "" {
			m["tab_label"] = d.tabLabel
		}
	default:
		return nil, newError("invalid_params", "pane.move requires a valid destination")
	}
	return m, nil
}

type paneMoveParams struct {
	PaneID      string         `json:"pane_id"`
	Destination map[string]any `json:"destination"`
	Focus       bool           `json:"focus"`
}

// PaneMoveResult is the pane.move payload. Pane carries the authoritative
// replacement pane (its id may differ from the original after a cross-workspace
// move); callers must continue with Pane.PaneID.
type PaneMoveResult struct {
	Type       string `json:"type"`
	MoveResult struct {
		Changed             bool       `json:"changed"`
		Pane                Pane       `json:"pane"`
		PreviousPaneID      string     `json:"previous_pane_id"`
		PreviousTabID       string     `json:"previous_tab_id"`
		PreviousWorkspaceID string     `json:"previous_workspace_id"`
		FocusedPaneID       string     `json:"focused_pane_id"`
		ClosedTabID         string     `json:"closed_tab_id,omitempty"`
		ClosedWorkspaceID   string     `json:"closed_workspace_id,omitempty"`
		CreatedTab          *Tab       `json:"created_tab,omitempty"`
		CreatedWorkspace    *Workspace `json:"created_workspace,omitempty"`
	} `json:"move_result"`
}

// NewPaneID is the id to use after the move; it may differ from the original.
func (r *PaneMoveResult) NewPaneID() string { return r.MoveResult.Pane.PaneID }

// PaneMove moves a pane to a typed destination and returns the move result,
// including the possibly-changed replacement pane id.
func (c *Client) PaneMove(ctx context.Context, paneID string, dest PaneMoveDestination, focus bool) (*PaneMoveResult, error) {
	if paneID == "" {
		return nil, newError("invalid_params", "pane.move requires a pane id")
	}
	wire, err := dest.wire()
	if err != nil {
		return nil, err
	}
	var res PaneMoveResult
	err = c.call(ctx, "pane.move", paneMoveParams{PaneID: paneID, Destination: wire, Focus: focus}, "pane_move", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// PaneClose closes a pane. Gate behind a confirmation nonce at the HTTP layer.
func (c *Client) PaneClose(ctx context.Context, paneID string) error {
	if paneID == "" {
		return newError("invalid_params", "pane.close requires a pane id")
	}
	return c.call(ctx, "pane.close", paneTarget{PaneID: paneID}, "ok", nil)
}

// ---- Worktrees ---------------------------------------------------------------

// WorktreeCreated is the result of creating a worktree.
type WorktreeCreated struct {
	Type      string    `json:"type"`
	Worktree  Worktree  `json:"worktree"`
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

type worktreeCreateParams struct {
	WorkspaceID *string `json:"workspace_id,omitempty"`
	CWD         *string `json:"cwd,omitempty"`
	Branch      *string `json:"branch,omitempty"`
	Base        *string `json:"base,omitempty"`
	Path        *string `json:"path,omitempty"`
	Label       *string `json:"label,omitempty"`
	Focus       bool    `json:"focus"`
}

// WorktreeCreateOptions configure worktree creation. Exactly one of WorkspaceID
// or CWD locates the source repository.
type WorktreeCreateOptions struct {
	WorkspaceID string
	CWD         string
	Branch      string
	Base        string
	Path        string
	Label       string
	Focus       bool
}

// WorktreeCreate creates a new git worktree and opens it as a workspace.
func (c *Client) WorktreeCreate(ctx context.Context, opts WorktreeCreateOptions) (*WorktreeCreated, error) {
	if opts.WorkspaceID == "" && opts.CWD == "" {
		return nil, newError("invalid_params", "worktree.create requires a workspace id or cwd")
	}
	var res WorktreeCreated
	err := c.call(ctx, "worktree.create", worktreeCreateParams{
		WorkspaceID: optStr(opts.WorkspaceID),
		CWD:         optStr(opts.CWD),
		Branch:      optStr(opts.Branch),
		Base:        optStr(opts.Base),
		Path:        optStr(opts.Path),
		Label:       optStr(opts.Label),
		Focus:       opts.Focus,
	}, "worktree_created", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// WorktreeOpened is the result of opening an existing worktree.
type WorktreeOpened struct {
	Type        string    `json:"type"`
	AlreadyOpen bool      `json:"already_open"`
	Worktree    Worktree  `json:"worktree"`
	Workspace   Workspace `json:"workspace"`
	Tab         Tab       `json:"tab"`
	RootPane    Pane      `json:"root_pane"`
}

type worktreeOpenParams struct {
	WorkspaceID *string `json:"workspace_id,omitempty"`
	CWD         *string `json:"cwd,omitempty"`
	Branch      *string `json:"branch,omitempty"`
	Path        *string `json:"path,omitempty"`
	Label       *string `json:"label,omitempty"`
	Focus       bool    `json:"focus"`
}

// WorktreeOpenOptions configure opening a worktree. Exactly one of Path or
// Branch selects the worktree.
type WorktreeOpenOptions struct {
	WorkspaceID string
	CWD         string
	Branch      string
	Path        string
	Label       string
	Focus       bool
}

// WorktreeOpen opens an existing worktree as a workspace.
func (c *Client) WorktreeOpen(ctx context.Context, opts WorktreeOpenOptions) (*WorktreeOpened, error) {
	if opts.Path == "" && opts.Branch == "" {
		return nil, newError("invalid_params", "worktree.open requires a path or branch")
	}
	var res WorktreeOpened
	err := c.call(ctx, "worktree.open", worktreeOpenParams{
		WorkspaceID: optStr(opts.WorkspaceID),
		CWD:         optStr(opts.CWD),
		Branch:      optStr(opts.Branch),
		Path:        optStr(opts.Path),
		Label:       optStr(opts.Label),
		Focus:       opts.Focus,
	}, "worktree_opened", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// WorktreeRemoved is the result of removing a worktree.
type WorktreeRemoved struct {
	Type        string `json:"type"`
	Forced      bool   `json:"forced"`
	Path        string `json:"path"`
	WorkspaceID string `json:"workspace_id"`
}

type worktreeRemoveParams struct {
	WorkspaceID string `json:"workspace_id"`
	Force       bool   `json:"force"`
}

// WorktreeRemove removes a worktree-backed workspace. Force is required for a
// dirty worktree and must map to a second, explicit force confirmation at the
// HTTP layer.
func (c *Client) WorktreeRemove(ctx context.Context, workspaceID string, force bool) (*WorktreeRemoved, error) {
	if workspaceID == "" {
		return nil, newError("invalid_params", "worktree.remove requires a workspace id")
	}
	var res WorktreeRemoved
	err := c.call(ctx, "worktree.remove",
		worktreeRemoveParams{WorkspaceID: workspaceID, Force: force}, "worktree_removed", &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
