// Package interpret turns already-rendered agent terminal text into an
// explicitly heuristic, chat-shaped reading of what the agent appears to be
// doing (SPEC §12.2).
//
// # This package is a guess, by construction
//
// Herdr publishes no semantic conversation data (SPEC §3.1). Everything here is
// pattern matching against the on-screen output of a third-party TUI, so it is
// wrong the moment that TUI changes its layout. Nothing it returns is
// authoritative, the relay advertises it as `heuristic_interpretation` rather
// than as any `structured_*` capability, and it is only reachable when an
// operator explicitly sets `[experimental] agent_output_parsing = true`.
//
// The corollary is a hard rule: a parser must return *no match* rather than a
// plausible one. An invented turn is worse than a raw terminal dump, because the
// operator cannot tell it apart from something the agent actually said.
//
// # Input shape
//
// The input is what `internal/server` already holds: pane text read with Herdr's
// `format: text`, then passed through security.SanitizeTextBlock and byte-bounded
// by boundObservedText. That means the parser can rely on there being
//
//   - no ANSI escapes, no CR, and no other C0/C1 control bytes,
//   - valid UTF-8,
//   - a bounded byte length,
//
// and therefore never has to emulate a terminal. It is reading a rendered screen
// grid, one line per row, including the TUI's own chrome.
//
// Because the highlight state of a selection row is carried by SGR styling, and
// styling is exactly what `format: text` discards, some prompts are detectable
// but not answerable. That is reported honestly via Option.SendKey and
// Interaction.Answerable rather than papered over — see opencode.go.
//
// # Output safety
//
// Every string that leaves this package originates in untrusted pane content, so
// callers must treat it as display text only. Two properties are enforced here so
// that a caller cannot forget them:
//
//   - Text is re-sanitized and length-bounded on the way out, even though the
//     input was already sanitized. This is defence in depth, not redundancy: it
//     keeps the guarantee local to the package that emits the field.
//   - Option.SendKey is *synthesized* from a parsed ordinal and validated against
//     a single-digit allowlist. It is never lifted from screen text. A pane can
//     therefore influence which options are offered, but never what bytes the
//     phone would send.
//
// Parsing is a single linear scan with bounded work and no backtracking patterns.
// It must never panic; fuzz_test.go asserts termination, bounds, and output
// sanitization.
package interpret
