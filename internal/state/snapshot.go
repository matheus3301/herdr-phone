package state

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// Snapshot is the versioned, hashed application view of the Herdr topology. It
// is the unit broadcast to clients.
type Snapshot struct {
	// Seq increments once per broadcast-worthy change.
	Seq uint64 `json:"seq"`
	// Hash is the stable content hash of the meaningful topology (excluding
	// volatile per-pane revision/scroll, which the terminal stream owns).
	Hash string `json:"hash"`
	// Topology is the normalized Herdr snapshot.
	Topology *herdr.Snapshot `json:"topology"`
	// Generations maps a live pane id to its lifecycle generation.
	Generations map[string]uint64 `json:"generations"`

	// bytes is the serialized wire size, used for queue byte-bounding.
	bytes int
}

// Bytes returns the serialized size of this snapshot as broadcast.
func (s *Snapshot) Bytes() int { return s.bytes }

// hashProjection is the stable, deterministic slice of topology the content
// hash covers. Fields that churn without a UI-meaningful change (pane revision,
// scrollback offsets) are intentionally excluded so terminal output alone does
// not force a topology rebroadcast.
type hashProjection struct {
	FW, FT, FP string
	Workspaces []projWS
	Tabs       []projTab
	Panes      []projPane
	Agents     []projAgent
	Layouts    []herdr.Layout
	Worktrees  []herdr.Worktree
}

type projWS struct {
	ID, Label, ActiveTab, Status string
	Number, PaneCount, TabCount  int
	Focused                      bool
	// Worktree is the workspace's checkout provenance. It is part of the hashed
	// content because a run's repository context is UI-meaningful: a workspace
	// that moves to another checkout must rebroadcast.
	Worktree string
}

type projTab struct {
	ID, WS, Label, Status string
	Number, PaneCount     int
	Focused               bool
}

type projPane struct {
	ID, Terminal, WS, Tab, Agent, DisplayAgent, Status, Label, Title, CWD, FgCWD, Occupant string
	Focused                                                                                bool
}

type projAgent struct {
	Pane, Agent, Name, Status, WS, Tab, CWD string
	Focused, InteractiveReady               bool
}

// project builds the hash projection with every slice sorted by a stable key.
// Herdr may enumerate topology arrays in a nondeterministic (map) order;
// canonicalizing here means identical content hashes identically regardless of
// wire order, so the engine does not rebroadcast a full snapshot every poll.
// Meaningful reordering is still captured because the stable order/identity
// fields (workspace/tab Number, layout geometry) are part of the hashed content.
func project(s *herdr.Snapshot) hashProjection {
	p := hashProjection{
		FW: s.FocusedWorkspaceID, FT: s.FocusedTabID, FP: s.FocusedPaneID,
	}
	for _, w := range s.Workspaces {
		p.Workspaces = append(p.Workspaces, projWS{
			ID: w.WorkspaceID, Label: w.Label, ActiveTab: w.ActiveTabID, Status: string(w.AgentStatus),
			Number: w.Number, PaneCount: w.PaneCount, TabCount: w.TabCount, Focused: w.Focused,
			Worktree: worktreeKey(w.Worktree),
		})
	}
	for _, t := range s.Tabs {
		p.Tabs = append(p.Tabs, projTab{
			ID: t.TabID, WS: t.WorkspaceID, Label: t.Label, Status: string(t.AgentStatus),
			Number: t.Number, PaneCount: t.PaneCount, Focused: t.Focused,
		})
	}
	for _, pane := range s.Panes {
		p.Panes = append(p.Panes, projPane{
			ID: pane.PaneID, Terminal: pane.TerminalID, WS: pane.WorkspaceID, Tab: pane.TabID,
			Agent: pane.Agent, DisplayAgent: pane.DisplayAgent, Status: string(pane.AgentStatus),
			Label: pane.Label, Title: pane.Title, CWD: pane.CWD, FgCWD: pane.ForegroundCWD,
			Occupant: pane.OccupantFingerprint(), Focused: pane.Focused,
		})
	}
	for _, a := range s.Agents {
		p.Agents = append(p.Agents, projAgent{
			Pane: a.PaneID, Agent: a.Agent, Name: a.Name, Status: string(a.AgentStatus),
			WS: a.WorkspaceID, Tab: a.TabID, CWD: a.CWD, Focused: a.Focused,
			InteractiveReady: a.InteractiveReady,
		})
	}
	// Copy Layouts/Worktrees before sorting so the shared input snapshot (which
	// becomes the broadcast Topology) is never mutated.
	p.Layouts = canonicalLayouts(s.Layouts)
	p.Worktrees = slices.Clone(s.Worktrees)

	slices.SortFunc(p.Workspaces, func(a, b projWS) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Tabs, func(a, b projTab) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Panes, func(a, b projPane) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Agents, func(a, b projAgent) int {
		if c := cmp.Compare(a.Pane, b.Pane); c != 0 {
			return c
		}
		return cmp.Compare(a.Agent, b.Agent)
	})
	slices.SortFunc(p.Layouts, func(a, b herdr.Layout) int { return cmp.Compare(a.TabID, b.TabID) })
	slices.SortFunc(p.Worktrees, func(a, b herdr.Worktree) int { return cmp.Compare(a.Path, b.Path) })
	return p
}

// worktreeKey renders a workspace's checkout provenance as one stable hash key.
// A nil worktree (a workspace outside a git work tree) hashes as the empty
// string, distinct from any real checkout.
func worktreeKey(w *herdr.WorkspaceWorktree) string {
	if w == nil {
		return ""
	}
	return w.RepoKey + "\x00" + w.RepoName + "\x00" + w.RepoRoot + "\x00" + w.CheckoutPath +
		"\x00" + strconv.FormatBool(w.IsLinkedWorktree)
}

// canonicalLayouts returns a deep-enough copy of layouts with each layout's
// inner pane list and split list sorted by a stable key, so map-ordered inner
// arrays also hash deterministically without mutating the input.
func canonicalLayouts(in []herdr.Layout) []herdr.Layout {
	if len(in) == 0 {
		return nil
	}
	out := make([]herdr.Layout, len(in))
	for i, l := range in {
		l.Panes = slices.Clone(l.Panes)
		slices.SortFunc(l.Panes, func(a, b herdr.LayoutPane) int { return cmp.Compare(a.PaneID, b.PaneID) })
		l.Splits = slices.Clone(l.Splits)
		slices.SortFunc(l.Splits, func(a, b herdr.LayoutSplit) int { return cmp.Compare(a.ID, b.ID) })
		out[i] = l
	}
	return out
}

// hashTopology computes the stable content hash for a topology.
func hashTopology(s *herdr.Snapshot) string {
	b, err := json.Marshal(project(s))
	if err != nil {
		// project() contains only serializable types; this cannot fail, but a
		// distinct sentinel keeps a hypothetical failure from colliding with a
		// real hash and forces a rebroadcast.
		return "hash-error"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
