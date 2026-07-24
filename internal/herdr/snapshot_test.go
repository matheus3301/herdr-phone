package herdr

import (
	"context"
	"encoding/json"
	"testing"
)

// liveSnapshotResult mirrors a trimmed but realistic session.snapshot payload
// captured from Herdr 0.7.5 (protocol 17).
const liveSnapshotJSON = `{
  "type": "session_snapshot",
  "snapshot": {
    "version": "0.7.5",
    "protocol": 17,
    "focused_workspace_id": "w6",
    "focused_tab_id": "w6:t1",
    "focused_pane_id": "w6:p1",
    "workspaces": [
      {"workspace_id":"w5","number":1,"label":"www","focused":false,"pane_count":13,"tab_count":8,"active_tab_id":"w5:t2","agent_status":"working"},
      {"workspace_id":"w6","number":2,"label":"herdr-phone","focused":true,"pane_count":8,"tab_count":7,"active_tab_id":"w6:t1","agent_status":"idle"}
    ],
    "tabs": [
      {"tab_id":"w6:t1","workspace_id":"w6","number":1,"label":"1","focused":true,"pane_count":2,"agent_status":"idle"}
    ],
    "panes": [
      {"pane_id":"w5:p1","terminal_id":"term_abc","workspace_id":"w5","tab_id":"w5:t1","focused":false,"cwd":"/Users/m/www","foreground_cwd":"/Users/m/www","agent":"opencode","agent_status":"idle","agent_session":{"source":"herdr:opencode","agent":"opencode","kind":"id","value":"sess-1"},"terminal_title":"OC","scroll":{"offset_from_bottom":0,"max_offset_from_bottom":0,"viewport_rows":53},"revision":27}
    ],
    "layouts": [
      {"workspace_id":"w5","tab_id":"w5:t1","zoomed":false,"area":{"x":0,"y":0,"width":185,"height":55},"focused_pane_id":"w5:p1","panes":[{"pane_id":"w5:p1","focused":true,"rect":{"x":0,"y":0,"width":93,"height":55}}],"splits":[{"id":"split_root","direction":"right","ratio":0.5,"rect":{"x":0,"y":0,"width":185,"height":55}}]}
    ],
    "agents": [
      {"terminal_id":"term_abc","agent":"opencode","agent_status":"idle","workspace_id":"w5","tab_id":"w5:t1","pane_id":"w5:p1","focused":false,"state_change_seq":345,"cwd":"/Users/m/www","foreground_cwd":"/Users/m/www","interactive_ready":true,"revision":27}
    ]
  }
}`

func TestSnapshotDecode(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		if req["method"] != "session.snapshot" {
			return replyError(req, "not_found", "unexpected")
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(liveSnapshotJSON), &env); err != nil {
			t.Fatal(err)
		}
		return reply(req, env)
	})
	c := newTestClient(t, s)
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Protocol != 17 || snap.FocusedWorkspaceID != "w6" {
		t.Fatalf("bad snapshot header: %+v", snap)
	}
	if len(snap.Workspaces) != 2 || len(snap.Panes) != 1 || len(snap.Agents) != 1 || len(snap.Layouts) != 1 {
		t.Fatalf("unexpected counts: ws=%d panes=%d agents=%d layouts=%d",
			len(snap.Workspaces), len(snap.Panes), len(snap.Agents), len(snap.Layouts))
	}
	p := snap.Panes[0]
	if p.AgentStatus != StatusIdle || p.AgentSession == nil || p.AgentSession.Value != "sess-1" {
		t.Fatalf("pane agent session not decoded: %+v", p)
	}
	if snap.Workspaces[0].AgentStatus != StatusWorking {
		t.Fatalf("workspace status not decoded")
	}
	if snap.Layouts[0].Splits[0].Ratio != 0.5 {
		t.Fatalf("layout split ratio not decoded")
	}
	if !snap.Agents[0].InteractiveReady {
		t.Fatalf("agent interactive_ready not decoded")
	}
}

func TestSnapshotToleratesUnknownFields(t *testing.T) {
	t.Parallel()
	s := newServer(func(req map[string]any) []byte {
		return reply(req, map[string]any{
			"type":             "session_snapshot",
			"future_top_field": "ignored",
			"snapshot": map[string]any{
				"version":         "0.8.0",
				"protocol":        17,
				"brand_new_field": []any{1, 2, 3},
				"workspaces": []any{
					map[string]any{"workspace_id": "w1", "label": "x", "agent_status": "blocked", "unexpected": true},
				},
			},
		})
	})
	c := newTestClient(t, s)
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap.Workspaces) != 1 || snap.Workspaces[0].AgentStatus != StatusBlocked {
		t.Fatalf("unknown fields broke decode: %+v", snap)
	}
}

func TestAgentStatusActive(t *testing.T) {
	t.Parallel()
	cases := map[AgentStatus]bool{
		StatusWorking: true, StatusBlocked: true,
		StatusIdle: false, StatusDone: false, StatusUnknown: false,
	}
	for st, want := range cases {
		if st.Active() != want {
			t.Errorf("%s.Active()=%v want %v", st, st.Active(), want)
		}
	}
}
