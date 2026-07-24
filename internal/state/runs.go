package state

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// Runs is the run projection of the state engine: the authoritative identity,
// execution context, and status of every agent run in the current snapshot. It
// carries no output, no transcript, and no terminal content, so it is cheap to
// compute per request and safe to embed in a summary response.
//
// The projection is derived from the same snapshot and the same pane lifecycle
// generations the mutation guard uses, so a run's asserted generation and the
// generation a mutation is checked against can never disagree.

// RunWorktree is a run's git checkout provenance, taken verbatim from the
// workspace Herdr reports it for.
type RunWorktree struct {
	RepoName         string
	RepoRoot         string
	CheckoutPath     string
	IsLinkedWorktree bool
}

// Run is one agent run: a pane generation plus the agent incarnation occupying
// it, with the topology context needed to describe it without flattening the
// execution model.
type Run struct {
	// RunID is an opaque, stable handle for this run. It binds the pane id, the
	// pane generation, AND the occupant digest, because a generation alone is not
	// unique across pane recycling: updateGenerationsLocked drops a vanished
	// pane's entry, so a pane id that later reappears restarts at generation 1.
	// Folding the incarnation in means a new occupant can never inherit a dead
	// run's identity — and anything keyed on it (a React key, a client-side run
	// partition) can never inherit the dead run's content either. Callers must
	// treat it as opaque and address operations by PaneID plus PaneGeneration.
	RunID string
	// PaneID is the canonical pane identifier every operation is keyed on.
	PaneID string
	// PaneGeneration is the pane's current lifecycle generation.
	PaneGeneration uint64
	// AgentIncarnation is an opaque digest of the pane's current occupant
	// identity (terminal id, agent kind, and bound agent session). It changes
	// exactly when PaneGeneration changes.
	AgentIncarnation string

	WorkspaceID    string
	WorkspaceLabel string
	TabID          string
	TabLabel       string
	TerminalID     string

	// AgentKind is Herdr's agent identifier (for example "claude"); AgentName is
	// the mutable operator-assigned name, which is never dispatch identity.
	AgentKind    string
	AgentName    string
	DisplayAgent string
	// Title is Herdr's own computed pane/agent title when it reports one. It is
	// display text, never a semantic message.
	Title string

	// Status is one of herdr's five lifecycle states. Anything else is reported
	// as StatusUnknown; an unrecognized state must never read as completion.
	Status herdr.AgentStatus

	InteractiveReady bool
	LaunchPending    bool
	Focused          bool

	CWD           string
	ForegroundCWD string
	Worktree      *RunWorktree

	// Revision is the pane's monotonic output revision, and StateChangeSeq the
	// agent's monotonic lifecycle counter. Both let a client detect movement
	// without holding content.
	Revision       int64
	StateChangeSeq int64
}

// RunSet is the run-scoped view of exactly one snapshot: its content hash and
// the runs projected from it. Pairing them means a consumer can correlate a run
// list with the topology snapshot it came from without a second, possibly newer,
// read.
type RunSet struct {
	// SnapshotHash is the content hash of the snapshot Runs was projected from.
	// It is empty before the first successful poll.
	SnapshotHash string
	// Runs is ordered by pane id, so the projection is deterministic.
	Runs []Run
}

// Runs returns the run projection of the current snapshot. It returns a zero
// RunSet before the first successful poll. The engine's lock is held only to copy
// the snapshot pointer and the generation/occupant maps; projection happens on
// immutable data, and the hash comes straight off the snapshot rather than from a
// re-serialization.
func (e *Engine) Runs() RunSet {
	e.mu.Lock()
	snap := e.current
	gens := make(map[string]uint64, len(e.generations))
	occupants := make(map[string]string, len(e.occupants))
	for k, v := range e.generations {
		gens[k] = v
	}
	for k, v := range e.occupants {
		occupants[k] = v
	}
	e.mu.Unlock()

	if snap == nil || snap.Topology == nil {
		return RunSet{}
	}
	return RunSet{SnapshotHash: snap.Hash, Runs: projectRuns(snap.Topology, gens, occupants)}
}

// projectRuns builds the run list from an immutable topology plus the pane
// generation and occupant maps. A pane with no live generation is skipped: the
// relay cannot guard an operation against it, so it must not be presented as an
// addressable run.
func projectRuns(topo *herdr.Snapshot, gens map[string]uint64, occupants map[string]string) []Run {
	agents := make(map[string]herdr.Agent, len(topo.Agents))
	for _, a := range topo.Agents {
		// Panes hold at most one occupant; the first entry for a pane wins so a
		// duplicated wire entry cannot change the projection nondeterministically.
		if _, seen := agents[a.PaneID]; !seen {
			agents[a.PaneID] = a
		}
	}
	workspaces := make(map[string]herdr.Workspace, len(topo.Workspaces))
	for _, w := range topo.Workspaces {
		workspaces[w.WorkspaceID] = w
	}
	tabs := make(map[string]herdr.Tab, len(topo.Tabs))
	for _, t := range topo.Tabs {
		tabs[t.TabID] = t
	}

	out := make([]Run, 0, len(topo.Agents))
	for _, p := range topo.Panes {
		agent, hasAgent := agents[p.PaneID]
		if p.Agent == "" && !hasAgent {
			continue // an empty shell pane is not a run
		}
		gen, ok := gens[p.PaneID]
		if !ok {
			continue // no live generation: not addressable, so not a run
		}

		run := Run{
			PaneID:           p.PaneID,
			PaneGeneration:   gen,
			AgentIncarnation: incarnation(occupants[p.PaneID]),
			WorkspaceID:      p.WorkspaceID,
			TabID:            p.TabID,
			TerminalID:       p.TerminalID,
			AgentKind:        p.Agent,
			DisplayAgent:     p.DisplayAgent,
			Title:            p.Title,
			Status:           normalizeStatus(p.AgentStatus),
			Focused:          p.Focused,
			CWD:              p.CWD,
			ForegroundCWD:    p.ForegroundCWD,
			Revision:         p.Revision,
		}
		run.RunID = runID(run.PaneID, gen, run.AgentIncarnation)
		if hasAgent {
			// The agent list is authoritative for agent-scoped facts; the pane
			// entry is authoritative for topology. Only fill from the agent where
			// the pane cannot answer.
			run.AgentName = agent.Name
			run.InteractiveReady = agent.InteractiveReady
			run.LaunchPending = agent.LaunchPending
			run.StateChangeSeq = agent.StateChangeSeq
			if run.AgentKind == "" {
				run.AgentKind = agent.Agent
			}
			if run.DisplayAgent == "" {
				run.DisplayAgent = agent.DisplayAgent
			}
			if run.Title == "" {
				run.Title = agent.Title
			}
			if p.AgentStatus == "" {
				run.Status = normalizeStatus(agent.AgentStatus)
			}
		}
		if ws, ok := workspaces[p.WorkspaceID]; ok {
			run.WorkspaceLabel = ws.Label
			if wt := ws.Worktree; wt != nil {
				run.Worktree = &RunWorktree{
					RepoName:         wt.RepoName,
					RepoRoot:         wt.RepoRoot,
					CheckoutPath:     wt.CheckoutPath,
					IsLinkedWorktree: wt.IsLinkedWorktree,
				}
			}
		}
		if tab, ok := tabs[p.TabID]; ok {
			run.TabLabel = tab.Label
		}
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b Run) int { return cmp.Compare(a.PaneID, b.PaneID) })
	return out
}

// normalizeStatus closes the status set to Herdr's five lifecycle states. An
// unrecognized or absent value becomes StatusUnknown so a consumer can never
// read an unknown state as completion.
func normalizeStatus(s herdr.AgentStatus) herdr.AgentStatus {
	switch s {
	case herdr.StatusIdle, herdr.StatusWorking, herdr.StatusBlocked, herdr.StatusDone:
		return s
	default:
		return herdr.StatusUnknown
	}
}

// runID builds the opaque run handle. Pane id and generation make it readable
// in a log; the occupant digest makes it unique across pane recycling, which
// generation alone is not. An empty incarnation (a pane with no occupant
// fingerprint) simply omits that component rather than inventing one.
func runID(paneID string, gen uint64, incarnation string) string {
	id := paneID + "@" + strconv.FormatUint(gen, 10)
	if incarnation == "" {
		return id
	}
	return id + "#" + incarnation
}

// incarnationDigestLen is how much of the occupant digest is exposed. 16 hex
// characters (64 bits) is far more than enough to detect a changed occupant,
// while keeping the handle short.
const incarnationDigestLen = 16

// incarnation digests a pane's occupant fingerprint. The fingerprint embeds the
// agent session reference, which may be a filesystem path, so it is hashed
// rather than published.
func incarnation(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:])[:incarnationDigestLen]
}
