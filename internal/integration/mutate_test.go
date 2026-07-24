package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

func dispatchClient(t *testing.T) (*herdr.Client, *fakeHerdr) {
	t.Helper()
	f := startFakeHerdr(t)
	return dispatchClientFor(f), f
}

// dispatchClientFor builds a Herdr client aimed at an existing fake server.
func dispatchClientFor(f *fakeHerdr) *herdr.Client {
	return herdr.NewClient(herdr.NewUnixDialer(f.path))
}

// TestDispatchMapsOperationsToHerdr drives a representative set of operations
// through the dispatcher and asserts each reached the right Herdr method with
// the right params.
func TestDispatchMapsOperationsToHerdr(t *testing.T) {
	c, f := dispatchClient(t)
	ctx := context.Background()

	cases := []struct {
		op     string
		params string
		method string // Herdr wire method expected
		verify func(t *testing.T, params json.RawMessage)
	}{
		{"workspace.create", `{"label":"api","focus":true}`, "workspace.create", func(t *testing.T, p json.RawMessage) {
			if got := stringField(p, "label"); got != "api" {
				t.Errorf("label = %q", got)
			}
		}},
		{"workspace.close", `{"workspace_id":"ws1"}`, "workspace.close", nil},
		{"tab.move", `{"tab_id":"t1","insert_index":2}`, "tab.move", func(t *testing.T, p json.RawMessage) {
			var m map[string]any
			_ = json.Unmarshal(p, &m)
			if m["insert_index"].(float64) != 2 {
				t.Errorf("insert_index = %v", m["insert_index"])
			}
		}},
		{"pane.split", `{"pane_id":"p1","direction":"right"}`, "pane.split", func(t *testing.T, p json.RawMessage) {
			if got := stringField(p, "target_pane_id"); got != "p1" {
				t.Errorf("target_pane_id = %q (pane_id should map to it)", got)
			}
		}},
		{"pane.zoom", `{"pane_id":"p1"}`, "pane.zoom", func(t *testing.T, p json.RawMessage) {
			if got := stringField(p, "mode"); got != "toggle" {
				t.Errorf("default zoom mode = %q, want toggle", got)
			}
		}},
		{"pane.move", `{"pane_id":"p1","destination":{"type":"new_tab","label":"x"}}`, "pane.move", func(t *testing.T, p json.RawMessage) {
			var m struct {
				Destination map[string]any `json:"destination"`
			}
			_ = json.Unmarshal(p, &m)
			if m.Destination["type"] != "new_tab" {
				t.Errorf("destination type = %v", m.Destination["type"])
			}
		}},
		{"agent.prompt", `{"pane_id":"p1","text":"hello"}`, "agent.prompt", func(t *testing.T, p json.RawMessage) {
			if got := stringField(p, "target"); got != "p1" {
				t.Errorf("target = %q (pane_id should map to it)", got)
			}
			if got := stringField(p, "text"); got != "hello" {
				t.Errorf("text = %q", got)
			}
		}},
		{"agent.send_keys", `{"pane_id":"p1","keys":["ctrl+c","enter"]}`, "agent.send_keys", func(t *testing.T, p json.RawMessage) {
			var m struct {
				Keys []string `json:"keys"`
			}
			_ = json.Unmarshal(p, &m)
			if len(m.Keys) != 2 || m.Keys[0] != "ctrl+c" {
				t.Errorf("keys = %v", m.Keys)
			}
		}},
		{"agent.start", `{"pane_id":"p1","kind":"claude","name":"agent1"}`, "agent.start", func(t *testing.T, p json.RawMessage) {
			if got := stringField(p, "kind"); got != "claude" {
				t.Errorf("kind = %q", got)
			}
		}},
		{"worktree.remove_force", `{"worktree_id":"ws9"}`, "worktree.remove", func(t *testing.T, p json.RawMessage) {
			var m struct {
				WorkspaceID string `json:"workspace_id"`
				Force       bool   `json:"force"`
			}
			_ = json.Unmarshal(p, &m)
			if !m.Force {
				t.Error("remove_force must set force=true")
			}
			// worktree_id (the open workspace id) becomes Herdr's workspace_id.
			if m.WorkspaceID != "ws9" {
				t.Errorf("worktree_id should map to workspace_id, got %q", m.WorkspaceID)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			res, err := dispatchMutation(ctx, c, tc.op, json.RawMessage(tc.params))
			if err != nil {
				t.Fatalf("dispatch %s: %v", tc.op, err)
			}
			if len(res) == 0 {
				t.Fatalf("empty result for %s", tc.op)
			}
			got := f.params(tc.method)
			if got == nil {
				t.Fatalf("herdr method %q was not called", tc.method)
			}
			if tc.verify != nil {
				tc.verify(t, got)
			}
		})
	}
}

func TestDispatchRejectsUnknownOperation(t *testing.T) {
	c, _ := dispatchClient(t)
	_, err := dispatchMutation(context.Background(), c, "server.stop", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for un-allowlisted operation")
	}
}

func TestDispatchInvalidParams(t *testing.T) {
	c, _ := dispatchClient(t)
	_, err := dispatchMutation(context.Background(), c, "workspace.create", json.RawMessage(`{"focus":"not-a-bool"}`))
	if err == nil {
		t.Fatal("expected decode error for bad params")
	}
}

func TestDispatchPaneMoveRequiresDestination(t *testing.T) {
	c, _ := dispatchClient(t)
	_, err := dispatchMutation(context.Background(), c, "pane.move", json.RawMessage(`{"pane_id":"p1"}`))
	if err == nil {
		t.Fatal("expected error for missing destination")
	}
}
