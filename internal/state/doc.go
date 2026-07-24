// Package state is the poll-as-truth Herdr state engine.
//
// It polls session.snapshot on a hot/cold cadence — fast (default 1.5s) while
// any agent is working or blocked or a browser is subscribed, relaxing to slow
// (default 12s) when idle. Herdr events are only wakeups: a wakeup triggers an
// immediate poll, and if a poll is already running exactly one follow-up is
// queued. A missed event therefore costs at most one interval, never state
// correctness.
//
// Each poll normalizes the topology into a versioned [Snapshot] with a stable
// content hash; only changes are broadcast. Every pane carries a lifecycle
// [Generation] that increments when its terminal occupant changes and resets
// (the id disappears) when it exits, closes, or moves to a new id. Mutations and
// terminal input carry an expected generation the server checks before dispatch.
//
// Per-client outbound queues are bounded by item count and bytes; consecutive
// snapshots coalesce to the newest, so a slow client is dropped rather than
// blocking the engine or other clients.
//
// All time is injected through [Clock] for deterministic tests.
package state
