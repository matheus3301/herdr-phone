package integration

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/buildinfo"
	"github.com/matheus3301/herdr-phone/internal/herdr"
	"github.com/matheus3301/herdr-phone/internal/server"
	"github.com/matheus3301/herdr-phone/internal/state"
	"github.com/matheus3301/herdr-phone/internal/tunnel"
)

// ---- state ----------------------------------------------------------------

// stateAdapter presents the state engine (topology, generations) and the Herdr
// client (bounded pane reads, handshake capabilities) as the server's
// StateProvider. The state engine remains the source of truth for topology; pane
// reads go straight to Herdr because they are not part of the broadcast snapshot.
type stateAdapter struct {
	engine   *state.Engine
	client   *herdr.Client
	capsBase capabilitiesBase
	kinds    *agentKinds
	now      func() time.Time
}

var _ server.StateProvider = (*stateAdapter)(nil)

func newStateAdapter(engine *state.Engine, client *herdr.Client, capsBase capabilitiesBase, kinds *agentKinds, now func() time.Time) *stateAdapter {
	if now == nil {
		now = time.Now
	}
	return &stateAdapter{engine: engine, client: client, capsBase: capsBase, kinds: kinds, now: now}
}

func (s *stateAdapter) toServer(snap *state.Snapshot) server.Snapshot {
	if snap == nil {
		return server.Snapshot{Data: json.RawMessage("null"), UpdatedAt: s.now()}
	}
	data, err := json.Marshal(snap)
	if err != nil {
		data = json.RawMessage("null")
	}
	return server.Snapshot{
		Version:   int(snap.Seq),
		Hash:      snap.Hash,
		Data:      data,
		UpdatedAt: s.now(),
	}
}

func (s *stateAdapter) Snapshot() server.Snapshot {
	return s.toServer(s.engine.Current())
}

func (s *stateAdapter) Subscribe(fn func(server.Snapshot)) func() {
	sub := s.engine.Subscribe()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sub.Done():
				return
			case <-sub.Notify():
				for _, snap := range sub.Drain() {
					fn(s.toServer(snap))
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			s.engine.Unsubscribe(sub)
		})
	}
}

func (s *stateAdapter) Generation(paneID string) (uint64, bool) {
	return s.engine.Generation(paneID)
}

// Capabilities returns the handshake facts plus authoritative startable agent
// kinds, recomputed per call so the kinds reflect the bounded discovery cache.
func (s *stateAdapter) Capabilities() json.RawMessage {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.capsBase.capabilitiesJSON(ctx, s.kinds)
}

// Runs maps the state engine's run projection onto the server's wire type. The
// mapping is total and mechanical: no field is synthesized, inferred, or
// defaulted here, so the server can never present something the engine did not
// observe. Content-free by construction — the projection holds no output.
func (s *stateAdapter) Runs() server.RunProjection {
	set := s.engine.Runs()
	if len(set.Runs) == 0 {
		return server.RunProjection{SnapshotHash: set.SnapshotHash}
	}
	out := make([]server.RunSummary, 0, len(set.Runs))
	for _, r := range set.Runs {
		summary := server.RunSummary{
			RunID:            r.RunID,
			PaneID:           r.PaneID,
			PaneGeneration:   r.PaneGeneration,
			AgentIncarnation: r.AgentIncarnation,
			WorkspaceID:      r.WorkspaceID,
			WorkspaceLabel:   r.WorkspaceLabel,
			TabID:            r.TabID,
			TabLabel:         r.TabLabel,
			TerminalID:       r.TerminalID,
			AgentKind:        r.AgentKind,
			AgentName:        r.AgentName,
			DisplayAgent:     r.DisplayAgent,
			Title:            r.Title,
			Status:           string(r.Status),
			InteractiveReady: r.InteractiveReady,
			LaunchPending:    r.LaunchPending,
			Focused:          r.Focused,
			CWD:              r.CWD,
			ForegroundCWD:    r.ForegroundCWD,
			Revision:         r.Revision,
			StateChangeSeq:   r.StateChangeSeq,
		}
		if wt := r.Worktree; wt != nil {
			summary.Worktree = &server.RunWorktree{
				RepoName:         wt.RepoName,
				RepoRoot:         wt.RepoRoot,
				CheckoutPath:     wt.CheckoutPath,
				IsLinkedWorktree: wt.IsLinkedWorktree,
			}
		}
		out = append(out, summary)
	}
	return server.RunProjection{SnapshotHash: set.SnapshotHash, Runs: out}
}

func (s *stateAdapter) ReadPane(ctx context.Context, paneID, source string, lines int) ([]byte, error) {
	src, err := herdr.ParseReadSource(source)
	if err != nil {
		return nil, err
	}
	res, err := s.client.PaneRead(ctx, paneID, herdr.PaneReadOptions{
		Source: src,
		Format: herdr.FormatText,
		Lines:  lines,
	})
	if err != nil {
		return nil, err
	}
	return []byte(res.Text), nil
}

// ---- mutator --------------------------------------------------------------

// mutatorAdapter dispatches an allowlisted operation name plus JSON params to the
// typed Herdr client (see mutate.go). The server has already enforced the
// allowlist, confirmation, generation, deadline, and idempotency by the time
// Mutate is called.
type mutatorAdapter struct {
	client *herdr.Client
	kinds  *agentKinds
}

var _ server.HerdrMutator = (*mutatorAdapter)(nil)

func (m *mutatorAdapter) Mutate(ctx context.Context, op string, params json.RawMessage) (json.RawMessage, error) {
	// agent.start must target a kind this Herdr build authoritatively reports it
	// can start. Validate against the discovery cache before any Herdr call so a
	// stale or guessed kind is rejected rather than attempted.
	if op == "agent.start" && m.kinds != nil {
		if err := m.kinds.validate(ctx, stringField(params, "kind")); err != nil {
			return nil, err
		}
	}
	return dispatchMutation(ctx, m.client, op, params)
}

// ---- daemon status --------------------------------------------------------

// daemonStatusAdapter exposes in-process daemon/subsystem health to
// authenticated sessions via the capabilities route. It never carries secrets
// and never exposes structural control (stop/rotate live only on the control
// socket).
type daemonStatusAdapter struct {
	mode    string
	ready   func() bool
	herdr   func() server.ComponentHealth
	stateFn func() server.ComponentHealth
}

var _ server.DaemonController = (*daemonStatusAdapter)(nil)

func (d *daemonStatusAdapter) Status() server.DaemonStatus {
	return server.DaemonStatus{
		Version:  buildinfo.Version,
		Protocol: buildinfo.HerdrProtocol,
		Mode:     d.mode,
		Ready:    d.ready(),
		Herdr:    d.herdr(),
		State:    d.stateFn(),
	}
}

// ---- tunnel status --------------------------------------------------------

// tunnelStatusAdapter reports bounded, secret-free cloudflared status.
type tunnelStatusAdapter struct {
	mode string
	sup  *tunnel.Supervisor
}

var _ server.TunnelStatus = (*tunnelStatusAdapter)(nil)

func (t *tunnelStatusAdapter) Tunnel() server.TunnelInfo {
	st := t.sup.State()
	return server.TunnelInfo{
		Mode:      t.mode,
		PublicURL: t.sup.URL(),
		Health: server.ComponentHealth{
			Healthy: st == tunnel.StateReady,
			Detail:  string(st),
		},
	}
}
