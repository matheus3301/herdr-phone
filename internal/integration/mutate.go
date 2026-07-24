package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// okResult is the JSON returned for operations that have no payload of their own.
var okResult = json.RawMessage(`{"ok":true}`)

// dispatchMutation maps an allowlisted operation name and its JSON params to the
// corresponding typed Herdr client call, returning the typed result marshaled to
// JSON. Every branch here corresponds to exactly one entry in the server's
// mutation allowlist; there is no generic passthrough, so a browser can never
// reach an un-allowlisted Herdr method. Params are decoded strictly per
// operation.
func dispatchMutation(ctx context.Context, c *herdr.Client, op string, raw json.RawMessage) (json.RawMessage, error) {
	switch op {

	// ---- workspaces -------------------------------------------------------
	case "workspace.create":
		var p struct {
			CWD   string            `json:"cwd"`
			Label string            `json:"label"`
			Env   map[string]string `json:"env"`
			Focus bool              `json:"focus"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.WorkspaceCreate(ctx, herdr.WorkspaceCreateOptions{CWD: p.CWD, Label: p.Label, Env: p.Env, Focus: p.Focus}))
	case "workspace.focus":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.WorkspaceFocus(ctx, p.WorkspaceID))
	case "workspace.rename":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.WorkspaceRename(ctx, p.WorkspaceID, p.Label))
	case "workspace.close":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return voidResult(c.WorkspaceClose(ctx, p.WorkspaceID))

	// ---- tabs -------------------------------------------------------------
	case "tab.create":
		var p struct {
			WorkspaceID string            `json:"workspace_id"`
			CWD         string            `json:"cwd"`
			Label       string            `json:"label"`
			Env         map[string]string `json:"env"`
			Focus       bool              `json:"focus"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.TabCreate(ctx, herdr.TabCreateOptions{WorkspaceID: p.WorkspaceID, CWD: p.CWD, Label: p.Label, Env: p.Env, Focus: p.Focus}))
	case "tab.focus":
		var p struct {
			TabID string `json:"tab_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.TabFocus(ctx, p.TabID))
	case "tab.rename":
		var p struct {
			TabID string `json:"tab_id"`
			Label string `json:"label"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.TabRename(ctx, p.TabID, p.Label))
	case "tab.move":
		var p struct {
			TabID       string `json:"tab_id"`
			InsertIndex int    `json:"insert_index"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		tabs, err := c.TabMove(ctx, p.TabID, p.InsertIndex)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"tabs": tabs})
	case "tab.close":
		var p struct {
			TabID string `json:"tab_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return voidResult(c.TabClose(ctx, p.TabID))

	// ---- panes ------------------------------------------------------------
	case "pane.focus":
		var p struct {
			PaneID string `json:"pane_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.PaneFocus(ctx, p.PaneID))
	case "pane.split":
		// The pane being split is the generation-guarded pane_id; there is no
		// separate target field, so server guard and dispatch address the same pane.
		var p struct {
			PaneID    string            `json:"pane_id"`
			Direction string            `json:"direction"`
			Ratio     float64           `json:"ratio"`
			CWD       string            `json:"cwd"`
			Env       map[string]string `json:"env"`
			Focus     bool              `json:"focus"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.PaneSplit(ctx, herdr.PaneSplitOptions{
			TargetPaneID: p.PaneID,
			Direction:    herdr.SplitDirection(p.Direction),
			Ratio:        p.Ratio,
			CWD:          p.CWD,
			Env:          p.Env,
			Focus:        p.Focus,
		}))
	case "pane.resize":
		var p struct {
			PaneID    string  `json:"pane_id"`
			Direction string  `json:"direction"`
			Amount    float64 `json:"amount"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.PaneResize(ctx, p.PaneID, herdr.PaneDirection(p.Direction), p.Amount))
	case "pane.zoom":
		var p struct {
			PaneID string `json:"pane_id"`
			Mode   string `json:"mode"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		mode := herdr.ZoomMode(p.Mode)
		if p.Mode == "" {
			mode = herdr.ZoomToggle
		}
		return result(c.PaneZoom(ctx, p.PaneID, mode))
	case "pane.swap":
		// pane_id is the generation-guarded pane; target_pane_id is the pane it
		// swaps with.
		var p struct {
			PaneID       string `json:"pane_id"`
			TargetPaneID string `json:"target_pane_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.PaneSwap(ctx, p.PaneID, p.TargetPaneID))
	case "pane.move":
		return paneMove(ctx, c, raw)
	case "pane.rename":
		var p struct {
			PaneID string `json:"pane_id"`
			Label  string `json:"label"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.PaneRename(ctx, p.PaneID, p.Label))
	case "pane.close":
		var p struct {
			PaneID string `json:"pane_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return voidResult(c.PaneClose(ctx, p.PaneID))

	// ---- agents -----------------------------------------------------------
	// Agent operations are canonically addressed by pane_id — the same field the
	// server generation-checks — so guard and dispatch always act on one pane.
	case "agent.focus":
		var p struct {
			PaneID string `json:"pane_id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.AgentFocus(ctx, p.PaneID))
	case "agent.prompt":
		var p struct {
			PaneID string `json:"pane_id"`
			Text   string `json:"text"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.AgentPrompt(ctx, p.PaneID, p.Text, herdr.PromptOptions{}))
	case "agent.send_keys":
		var p struct {
			PaneID string   `json:"pane_id"`
			Keys   []string `json:"keys"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return voidResult(c.AgentSendKeys(ctx, p.PaneID, p.Keys))
	case "agent.rename":
		var p struct {
			PaneID string `json:"pane_id"`
			Name   string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.AgentRename(ctx, p.PaneID, p.Name))
	case "agent.start":
		var p struct {
			PaneID string   `json:"pane_id"`
			Kind   string   `json:"kind"`
			Name   string   `json:"name"`
			Args   []string `json:"args"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.AgentStart(ctx, herdr.StartOptions{Name: p.Name, Kind: p.Kind, PaneID: p.PaneID, Args: p.Args}))

	// ---- worktrees --------------------------------------------------------
	case "worktree.create":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
			CWD         string `json:"cwd"`
			Branch      string `json:"branch"`
			Base        string `json:"base"`
			Path        string `json:"path"`
			Label       string `json:"label"`
			Focus       bool   `json:"focus"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.WorktreeCreate(ctx, herdr.WorktreeCreateOptions{
			WorkspaceID: p.WorkspaceID, CWD: p.CWD, Branch: p.Branch, Base: p.Base, Path: p.Path, Label: p.Label, Focus: p.Focus,
		}))
	case "worktree.open":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
			CWD         string `json:"cwd"`
			Branch      string `json:"branch"`
			Path        string `json:"path"`
			Label       string `json:"label"`
			Focus       bool   `json:"focus"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return result(c.WorktreeOpen(ctx, herdr.WorktreeOpenOptions{
			WorkspaceID: p.WorkspaceID, CWD: p.CWD, Branch: p.Branch, Path: p.Path, Label: p.Label, Focus: p.Focus,
		}))
	case "worktree.remove":
		return worktreeRemove(ctx, c, raw, false)
	case "worktree.remove_force":
		return worktreeRemove(ctx, c, raw, true)

	default:
		return nil, fmt.Errorf("integration: operation %q is not implemented", op)
	}
}

// worktreeRemove decodes the removal params and dispatches it. The removable
// resource the server guards and confirms is worktree_id, which carries the open
// workspace id backing the worktree — the value Herdr's worktree.remove expects —
// so the confirmation binding and the dispatched call address the same resource.
func worktreeRemove(ctx context.Context, c *herdr.Client, raw json.RawMessage, force bool) (json.RawMessage, error) {
	var p struct {
		WorktreeID string `json:"worktree_id"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	return result(c.WorktreeRemove(ctx, p.WorktreeID, force))
}

// paneMove decodes the typed pane.move destination and dispatches it.
func paneMove(ctx context.Context, c *herdr.Client, raw json.RawMessage) (json.RawMessage, error) {
	var p struct {
		PaneID      string `json:"pane_id"`
		Focus       bool   `json:"focus"`
		Destination struct {
			Type         string  `json:"type"`
			TabID        string  `json:"tab_id"`
			Split        string  `json:"split"`
			TargetPaneID string  `json:"target_pane_id"`
			Ratio        float64 `json:"ratio"`
			WorkspaceID  string  `json:"workspace_id"`
			Label        string  `json:"label"`
			TabLabel     string  `json:"tab_label"`
		} `json:"destination"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	var dest herdr.PaneMoveDestination
	switch p.Destination.Type {
	case "tab":
		dest = herdr.MoveToTab(p.Destination.TabID, herdr.SplitDirection(p.Destination.Split), p.Destination.TargetPaneID, p.Destination.Ratio)
	case "new_tab":
		dest = herdr.MoveToNewTab(p.Destination.WorkspaceID, p.Destination.Label)
	case "new_workspace":
		dest = herdr.MoveToNewWorkspace(p.Destination.Label, p.Destination.TabLabel)
	default:
		return nil, herdr.NewError(herdr.CodeInvalidParams, "pane.move requires a destination type of tab, new_tab, or new_workspace")
	}
	return result(c.PaneMove(ctx, p.PaneID, dest, p.Focus))
}

// ---- decode/marshal helpers ------------------------------------------------

// decode strictly unmarshals params into v, rejecting unknown fields and any
// trailing data so a browser cannot smuggle extra keys past the per-operation
// contract. Empty params decode as an empty object so operations with
// all-optional fields succeed with no body.
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return herdr.NewError(herdr.CodeInvalidParams, "invalid params: "+err.Error())
	}
	if dec.More() {
		return herdr.NewError(herdr.CodeInvalidParams, "invalid params: unexpected trailing data")
	}
	return nil
}

// result marshals a typed pointer result, propagating a non-nil error unchanged
// so the server preserves the structured Herdr failure.
func result[T any](v *T, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return marshal(v)
}

// voidResult returns the ok payload when err is nil.
func voidResult(err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return okResult, nil
}

func marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("integration: encode result: %w", err)
	}
	return b, nil
}

// stringField extracts a top-level string field from raw params, or "". It backs
// only the pre-dispatch agent-kind lookup; per-operation params are decoded
// strictly by decode.
func stringField(raw json.RawMessage, field string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}
