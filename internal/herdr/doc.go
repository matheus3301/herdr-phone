// Package herdr is the single typed owner of the Herdr socket wire protocol
// (protocols 17 and 19, schema version 1, Herdr 0.7.5 and 0.8.x).
//
// It speaks newline-delimited JSON over a Unix domain socket. Normal requests
// use one connection each with a bounded read, a string request id, result-type
// validation, and a default five-second deadline. A separate long-lived
// [Subscriber] keeps an events.subscribe connection open purely to wake the
// state engine; snapshots remain the source of truth.
//
// The package never forwards arbitrary browser-supplied method names or raw
// params: every request is constructed from a typed method here. Every mutation
// carries explicit resource ids and never relies on Herdr UI focus.
//
// All external time and I/O are injected through [Clock] and [Dialer] so the
// package is fully deterministic under test.
package herdr

// Protocols are the Herdr wire protocol versions this package supports, in
// ascending order. A version is added only after its full
// `herdr api schema --json` dump has been verified against these typed models:
// protocol 19 (Herdr 0.8.0) is strictly additive over protocol 17
// (Herdr 0.7.5) — one new method (workspace.move_block), one new event variant
// (workspace_reordered), and new integration-target enum values — so both
// decode with the same models. Unverified protocols (including the
// preview-only 18) stay rejected.
var Protocols = [...]int{17, 19}

// MinHerdrVersion is the oldest Herdr release whose APIs this package uses.
const MinHerdrVersion = "0.7.5"
