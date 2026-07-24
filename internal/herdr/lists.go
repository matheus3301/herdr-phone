package herdr

import "context"

// Explicit list reads. The state engine derives topology from the snapshot;
// these exist for targeted refreshes and for callers that want authoritative
// server ordering without a full snapshot.

type workspaceListResult struct {
	Type       string      `json:"type"`
	Workspaces []Workspace `json:"workspaces"`
}

// WorkspaceList returns all workspaces in server order.
func (c *Client) WorkspaceList(ctx context.Context) ([]Workspace, error) {
	var res workspaceListResult
	if err := c.call(ctx, "workspace.list", struct{}{}, "workspace_list", &res); err != nil {
		return nil, err
	}
	return res.Workspaces, nil
}

type tabListParams struct {
	WorkspaceID *string `json:"workspace_id,omitempty"`
}

type tabListResult struct {
	Type string `json:"type"`
	Tabs []Tab  `json:"tabs"`
}

// TabList returns tabs in authoritative server order. An empty workspaceID
// lists the focused workspace's tabs.
func (c *Client) TabList(ctx context.Context, workspaceID string) ([]Tab, error) {
	var res tabListResult
	if err := c.call(ctx, "tab.list", tabListParams{WorkspaceID: optStr(workspaceID)}, "tab_list", &res); err != nil {
		return nil, err
	}
	return res.Tabs, nil
}

type paneListParams struct {
	WorkspaceID *string `json:"workspace_id,omitempty"`
}

type paneListResult struct {
	Type  string `json:"type"`
	Panes []Pane `json:"panes"`
}

// PaneList returns panes for a workspace (or the focused workspace when empty).
func (c *Client) PaneList(ctx context.Context, workspaceID string) ([]Pane, error) {
	var res paneListResult
	if err := c.call(ctx, "pane.list", paneListParams{WorkspaceID: optStr(workspaceID)}, "pane_list", &res); err != nil {
		return nil, err
	}
	return res.Panes, nil
}
