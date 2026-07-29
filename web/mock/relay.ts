/**
 * Mock relay — dev + preview ONLY. A Vite plugin that stands in for the Go relay
 * so the real production bundle runs against a deterministic in-memory herd. It
 * is node-side only and never enters the browser bundle.
 *
 * It emits the EXACT backend wire shapes (see internal/server/**,
 * internal/state/snapshot.go, internal/herdr/models.go, internal/terminal/
 * protocol.go) AND enforces the same guards:
 *
 *   - the mutation allowlist, with each operation's canonical resource field;
 *   - strict per-operation params, so an unknown field (for example the
 *     dispatcher-preferred `target`) is rejected exactly as Go's
 *     DisallowUnknownFields rejects it;
 *   - a divergent alternate identifier is refused;
 *   - a mandatory, nonzero, matching `expected_generation` on every pane-scoped
 *     operation, on confirmations, and on a terminal attach;
 *   - single-use confirmation nonces bound to operation, resource, generation,
 *     and params.
 *
 * A mock that is laxer than production lets the frontend drift into sending
 * requests the real relay refuses, which is precisely the class of defect this
 * rewrite had to repair. If you change a guard in internal/server, change it
 * here in the same commit.
 */
import type { Plugin, ViteDevServer, PreviewServer } from "vite";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import { WebSocketServer, type WebSocket } from "ws";

const PAIR_SECRET = process.env.MOCK_PAIR_SECRET ?? "dev-pair-secret";
const COOKIE = "hp_mock_session";
const CSRF = "mock-csrf-token";

/* --------------------------------------------------------------- relay mode */

/**
 * Which relay mode the mock emulates (SPEC §9.1, DELIVERY-v0.3.0 §3).
 *
 * QUICK (the default, and what the Playwright journeys drive): the single-use
 * pairing secret is the only app gate — no cookie means 401 and `/pair` is the
 * only way in.
 *
 * NAMED: Cloudflare Access is the gate. There is no Access edge in front of the
 * mock, so clearing Access is modelled as "already cleared": a cookie-less request
 * to a session route is transparently given a session and a `Set-Cookie`, exactly
 * as `internal/server/routes.go` `provisionSession` does, and `identity.mode` is
 * `"named"`. `/pair` stays live for re-binding, as it does in production.
 *
 * Opt in with `MOCK_RELAY_MODE=named` (dev/preview) or at runtime with
 * `POST /api/v1/__mode {"mode":"named"}`. `__mode` also flips `access_denied`,
 * which stands in for an expired/invalid Access token: every authenticated route
 * then answers 401 `access denied`, the rejection the origin emits at middleware
 * step 2 before it ever looks at the app session.
 *
 * Experimental heuristic interpretation (SPEC §12.2) is OFF by default here, as it
 * is in config. `POST /api/v1/__interpretation {"enabled":true}` advertises
 * `heuristic_interpretation`, adds the two interpreted part types, and appends the
 * interpreted parts to a run read — always *alongside* the observed-output part,
 * never instead of it.
 */
type MockMode = "named" | "quick";
const ENV_MODE: MockMode = process.env.MOCK_RELAY_MODE === "named" ? "named" : "quick";
let mode: MockMode = ENV_MODE;
let accessDenied = false;

/** The Access subject a named-mode relay reports; quick mode has no identity. */
const NAMED_SUBJECT = "operator@example.com";

/** internal/server/pairing.go idJSON — the identity both /pair and /session emit. */
function identity() {
  return mode === "named"
    ? { subject: NAMED_SUBJECT, display: NAMED_SUBJECT, quick: false, mode: "named" }
    : { subject: "", display: "Quick Tunnel operator", quick: true, mode: "quick" };
}

/** internal/server/pairing.go pairResponse == sessionResponse. */
function sessionPayload() {
  return { csrf_token: CSRF, expires_unix_ms: Date.now() + 12 * 3600 * 1000, identity: identity() };
}

let clock = 1_780_000_000_000;
const now = () => clock++;

/**
 * `WorkspaceInfo.worktree` — the ONLY worktree context `session.snapshot`
 * carries (`WorkspaceWorktreeInfo` in `herdr api schema --json`, protocol 17).
 * Note the absence of a branch: there is none anywhere in a snapshot, so the
 * mock must not supply one either.
 */
interface WireWorkspaceWorktree {
  repo_key: string;
  repo_name: string;
  repo_root: string;
  checkout_path: string;
  is_linked_worktree: boolean;
}

interface WireWorkspace {
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  tab_count: number;
  active_tab_id: string;
  agent_status: string;
  worktree?: WireWorkspaceWorktree;
}
interface WireTab {
  tab_id: string;
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  agent_status: string;
}
interface WirePane {
  pane_id: string;
  terminal_id: string;
  workspace_id: string;
  tab_id: string;
  focused: boolean;
  cwd: string;
  foreground_cwd: string;
  agent?: string;
  display_agent?: string;
  agent_status?: string;
  label?: string;
  title?: string;
  terminal_title_stripped?: string;
  revision: number;
}
interface WireAgent {
  terminal_id: string;
  agent: string;
  name: string;
  agent_status: string;
  workspace_id: string;
  tab_id: string;
  pane_id: string;
  focused: boolean;
  interactive_ready: boolean;
  state_change_seq: number;
  cwd: string;
  foreground_cwd: string;
  terminal_title_stripped: string;
  revision: number;
}
interface WireLayout {
  workspace_id: string;
  tab_id: string;
  zoomed: boolean;
  area: { x: number; y: number; width: number; height: number };
  focused_pane_id: string;
  panes: Array<{ pane_id: string; focused: boolean; rect: { x: number; y: number; width: number; height: number } }>;
  splits: unknown[];
}
interface Herd {
  seq: number;
  idSeq: number;
  workspaces: WireWorkspace[];
  tabs: WireTab[];
  panes: WirePane[];
  agents: WireAgent[];
  layouts: WireLayout[];
  generations: Record<string, number>;
  focusedWorkspaceId: string;
  focusedTabId: string;
  focusedPaneId: string;
}

const FULL = { x: 0, y: 0, width: 1, height: 1 };

/** Build a workspace's checkout provenance the way Herdr reports it. */
function worktreeInfo(repoName: string, repoRoot: string, checkoutPath: string, linked: boolean): WireWorkspaceWorktree {
  return {
    repo_key: `key:${repoRoot}`,
    repo_name: repoName,
    repo_root: repoRoot,
    checkout_path: checkoutPath,
    is_linked_worktree: linked,
  };
}

/**
 * The seed covers every inbox section — blocked, working, done (Updated), idle,
 * and unknown — plus an empty shell pane, a linked worktree, and a main checkout,
 * so the journeys and the screenshots exercise the real vocabulary.
 */
function seed(): Herd {
  const workspaces: WireWorkspace[] = [
    {
      workspace_id: "w1", number: 1, label: "space-api", focused: true, pane_count: 3, tab_count: 2,
      active_tab_id: "w1:t1", agent_status: "blocked",
      worktree: worktreeInfo("space-api", "/Users/dev/code/space-api", "/Users/dev/code/space-api-auth", true),
    },
    {
      workspace_id: "w2", number: 2, label: "mobile-ui", focused: false, pane_count: 3, tab_count: 1,
      active_tab_id: "w2:t1", agent_status: "working",
      worktree: worktreeInfo("mobile-ui", "/Users/dev/code/mobile-ui", "/Users/dev/code/mobile-ui", false),
    },
    // w3 resolves to no git checkout at all — the `worktree` field is absent, not
    // null-with-empty-strings, exactly as Go's `omitempty` emits it.
    { workspace_id: "w3", number: 3, label: "infra", focused: false, pane_count: 2, tab_count: 1, active_tab_id: "w3:t1", agent_status: "idle" },
  ];
  const tabs: WireTab[] = [
    { tab_id: "w1:t1", workspace_id: "w1", number: 1, label: "auth-refactor", focused: true, pane_count: 2, agent_status: "blocked" },
    { tab_id: "w1:t2", workspace_id: "w1", number: 2, label: "tests", focused: false, pane_count: 1, agent_status: "done" },
    { tab_id: "w2:t1", workspace_id: "w2", number: 1, label: "app", focused: false, pane_count: 3, agent_status: "working" },
    { tab_id: "w3:t1", workspace_id: "w3", number: 1, label: "shell", focused: false, pane_count: 2, agent_status: "idle" },
  ];
  const api = "/Users/dev/code/space-api";
  const mobile = "/Users/dev/code/mobile-ui";
  const infra = "/Users/dev/code/infra";
  const panes: WirePane[] = [
    { pane_id: "w1:p1", terminal_id: "term_1", workspace_id: "w1", tab_id: "w1:t1", focused: true, cwd: api, foreground_cwd: api, agent: "claude", display_agent: "claude", agent_status: "blocked", revision: 3 },
    { pane_id: "w1:p2", terminal_id: "term_2", workspace_id: "w1", tab_id: "w1:t1", focused: false, cwd: api, foreground_cwd: api, title: "server", revision: 1 },
    { pane_id: "w1:p3", terminal_id: "term_3", workspace_id: "w1", tab_id: "w1:t2", focused: false, cwd: api, foreground_cwd: api, agent: "codex", display_agent: "codex", agent_status: "done", revision: 2 },
    { pane_id: "w2:p1", terminal_id: "term_4", workspace_id: "w2", tab_id: "w2:t1", focused: false, cwd: mobile, foreground_cwd: mobile, agent: "opencode", display_agent: "opencode", agent_status: "working", revision: 5 },
    { pane_id: "w2:p2", terminal_id: "term_5", workspace_id: "w2", tab_id: "w2:t1", focused: false, cwd: mobile, foreground_cwd: mobile, title: "vite", revision: 1 },
    { pane_id: "w2:p3", terminal_id: "term_7", workspace_id: "w2", tab_id: "w2:t1", focused: false, cwd: mobile, foreground_cwd: mobile, agent: "cursor", display_agent: "cursor", agent_status: "unknown", revision: 1 },
    { pane_id: "w3:p1", terminal_id: "term_6", workspace_id: "w3", tab_id: "w3:t1", focused: false, cwd: infra, foreground_cwd: infra, title: "zsh", revision: 1 },
    { pane_id: "w3:p2", terminal_id: "term_8", workspace_id: "w3", tab_id: "w3:t1", focused: false, cwd: infra, foreground_cwd: infra, agent: "gemini", display_agent: "gemini", agent_status: "idle", revision: 1 },
  ];
  const agents: WireAgent[] = [
    { terminal_id: "term_1", agent: "claude", name: "claude", agent_status: "blocked", workspace_id: "w1", tab_id: "w1:t1", pane_id: "w1:p1", focused: true, interactive_ready: true, state_change_seq: 40, cwd: api, foreground_cwd: api, terminal_title_stripped: "Approve this command?", revision: 3 },
    { terminal_id: "term_4", agent: "opencode", name: "opencode", agent_status: "working", workspace_id: "w2", tab_id: "w2:t1", pane_id: "w2:p1", focused: false, interactive_ready: true, state_change_seq: 30, cwd: mobile, foreground_cwd: mobile, terminal_title_stripped: "Refactoring mobile navigation", revision: 5 },
    { terminal_id: "term_3", agent: "codex", name: "codex", agent_status: "done", workspace_id: "w1", tab_id: "w1:t2", pane_id: "w1:p3", focused: false, interactive_ready: false, state_change_seq: 20, cwd: api, foreground_cwd: api, terminal_title_stripped: "api tests", revision: 2 },
    { terminal_id: "term_8", agent: "gemini", name: "gemini", agent_status: "idle", workspace_id: "w3", tab_id: "w3:t1", pane_id: "w3:p2", focused: false, interactive_ready: true, state_change_seq: 10, cwd: infra, foreground_cwd: infra, terminal_title_stripped: "waiting", revision: 1 },
    { terminal_id: "term_7", agent: "cursor", name: "cursor", agent_status: "unknown", workspace_id: "w2", tab_id: "w2:t1", pane_id: "w2:p3", focused: false, interactive_ready: false, state_change_seq: 5, cwd: mobile, foreground_cwd: mobile, terminal_title_stripped: "", revision: 1 },
  ];
  const layouts: WireLayout[] = [
    { workspace_id: "w1", tab_id: "w1:t1", zoomed: false, area: FULL, focused_pane_id: "w1:p1", panes: [{ pane_id: "w1:p1", focused: true, rect: FULL }, { pane_id: "w1:p2", focused: false, rect: FULL }], splits: [] },
  ];
  const generations: Record<string, number> = {
    "w1:p1": 3,
    "w1:p2": 1,
    "w1:p3": 2,
    "w2:p1": 5,
    "w2:p2": 1,
    "w2:p3": 1,
    "w3:p1": 1,
    "w3:p2": 1,
  };
  return { seq: 9, idSeq: 9, workspaces, tabs, panes, agents, layouts, generations, focusedWorkspaceId: "w1", focusedTabId: "w1:t1", focusedPaneId: "w1:p1" };
}

let herd = seed();

function topology() {
  return {
    version: "0.7.5",
    protocol: 17,
    focused_workspace_id: herd.focusedWorkspaceId,
    focused_tab_id: herd.focusedTabId,
    focused_pane_id: herd.focusedPaneId,
    workspaces: herd.workspaces,
    tabs: herd.tabs,
    panes: herd.panes,
    layouts: herd.layouts,
    agents: herd.agents,
  };
}

function envelope() {
  const data = { seq: herd.seq, hash: `h${herd.seq}`, topology: topology(), generations: herd.generations };
  return { version: herd.seq, hash: `h${herd.seq}`, data, updated_at: new Date(clock).toISOString() };
}

/* ------------------------------------------- structured run contract (§12.1) */

/**
 * The run contract's server-side bounds and switches. `runContract.supported`
 * models an OLDER relay when false: `/capabilities` then omits `runs` entirely
 * and both run routes 404, which is exactly what the browser must fail closed
 * against. `maxRuns` is lowered by a test hook to exercise list truncation.
 */
const runContract = {
  supported: true,
  contractVersion: 1,
  maxOutputLines: 400,
  maxOutputBytes: 65_536,
  maxRuns: 200,
};

const RUN_CONTRACT_VERSION = 1;
const PART_OBSERVED_TERMINAL_OUTPUT = "observed_terminal_output";
const PART_INTERPRETED_TRANSCRIPT = "interpreted_transcript";
const PART_INTERPRETED_INTERACTION = "interpreted_interaction";
const DEFAULT_RUN_OUTPUT_LINES = 200;
const RUN_OUTPUT_SOURCES = ["recent", "recent-unwrapped", "visible"];

/**
 * Experimental heuristic interpretation (SPEC §12.2), off by default exactly as in
 * `[experimental] agent_output_parsing`. `POST /api/v1/__interpretation` flips it,
 * so a journey can cover both the default contract and the chat view.
 *
 * The mock does NOT re-implement internal/interpret's grammars — that would test a
 * second parser rather than the wire contract. It emits the part shapes the Go
 * relay emits for the captured fixtures, which is what the browser must consume.
 */
const interpretation = {
  enabled: false,
  parsers: ["claude", "opencode"],
};

/** Mirrors internal/server/runs.go runCapabilities: every semantic flag false. */
function runCapabilities() {
  return {
    contract_version: RUN_CONTRACT_VERSION,
    supported: true,
    structured_messages: false,
    structured_tool_calls: false,
    structured_interactions: false,
    structured_diffs: false,
    structured_tests: false,
    structured_plans: false,
    observed_terminal_output: true,
    // Never a structured_* flag: interpretation is advertised as a guess.
    heuristic_interpretation: interpretation.enabled,
    ...(interpretation.enabled ? { interpretation_parsers: [...interpretation.parsers].sort() } : {}),
    part_types: interpretation.enabled
      ? [PART_OBSERVED_TERMINAL_OUTPUT, PART_INTERPRETED_TRANSCRIPT, PART_INTERPRETED_INTERACTION]
      : [PART_OBSERVED_TERMINAL_OUTPUT],
    output_sources: RUN_OUTPUT_SOURCES,
    max_output_bytes: runContract.maxOutputBytes,
    max_output_lines: runContract.maxOutputLines,
    max_runs: runContract.maxRuns,
  };
}

/**
 * The interpreted parts for one pane, mirroring what internal/server/runs.go emits
 * for the Claude Code 2.1.220 and OpenCode 1.18.4 fixtures.
 *
 * Returns [] when the feature is off or the pane's agent kind is not configured —
 * the same two gates the Go relay applies.
 */
function interpretedParts(paneId: string): unknown[] {
  if (!interpretation.enabled) return [];
  const run = projectRuns().find((r) => r.pane_id === paneId);
  const kind = run?.agent_kind ?? "";
  if (!interpretation.parsers.includes(kind)) return [];

  if (kind === "opencode") {
    // Detected, but never answerable: the highlighted button is carried by ANSI
    // styling that `format: text` discards, so no option gets a send_key.
    return [
      {
        type: PART_INTERPRETED_TRANSCRIPT,
        parser: "opencode",
        experimental: true,
        turns: [
          { kind: "agent_text", text: "I’ll verify the file and append the requested line." },
          { kind: "tool_call", tool: "Read", text: "notes.txt" },
          // A gutter-framed block is output of something OpenCode *ran*, never the
          // agent's own words (SPEC §12.2).
          { kind: "tool_result", text: "$ cat notes.txt" },
          { kind: "status", text: "Thought: Considering the file ending · 1.8s" },
        ],
        dropped_turns: 0,
        dropped_lines: 6,
      },
      {
        type: PART_INTERPRETED_INTERACTION,
        parser: "opencode",
        experimental: true,
        interaction: "approval",
        title: "Edit /tmp/sandbox/notes.txt",
        question: "Permission required",
        answerable: false,
        options: [{ label: "Allow once" }, { label: "Allow always" }, { label: "Reject" }],
        diff: [
          { line: 1, op: "context", text: "sample file for the fixture capture" },
          { line: 2, op: "add", text: "hello fixture" },
        ],
      },
    ];
  }

  return [
    {
      type: PART_INTERPRETED_TRANSCRIPT,
      parser: "claude",
      experimental: true,
      turns: [
        { kind: "agent_text", text: "I'll check the existing file ending, then append the line." },
        { kind: "tool_call", tool: "Bash", text: "ls -la && cat notes.txt" },
        { kind: "tool_result", text: "sample file for the fixture capture" },
        { kind: "status", text: "Worked for 4s · 139 tokens" },
      ],
      dropped_turns: 0,
      dropped_lines: 9,
      starts_mid_turn: false,
    },
    {
      type: PART_INTERPRETED_INTERACTION,
      parser: "claude",
      experimental: true,
      interaction: "approval",
      title: "Bash command",
      detail: ['echo "hello fixture" >> notes.txt', "Append line to notes.txt and verify"],
      question: "Do you want to proceed?",
      answerable: true,
      options: [
        { label: "Yes", send_key: "1" },
        { label: "Yes, and always allow access to sandbox/ from this project", send_key: "2" },
        { label: "No", send_key: "3" },
      ],
    },
  ];
}

/** A 16-hex-character digest of the pane's occupant, as internal/state does. */
function incarnation(paneId: string): string {
  const pane = herd.panes.find((p) => p.pane_id === paneId);
  const agent = herd.agents.find((a) => a.pane_id === paneId);
  const fingerprint = `${pane?.terminal_id ?? ""}|${pane?.agent ?? agent?.agent ?? ""}|${agent?.name ?? ""}|${herd.generations[paneId] ?? 0}`;
  let h1 = 0x811c9dc5;
  let h2 = 0x01000193;
  for (let i = 0; i < fingerprint.length; i++) {
    h1 = Math.imul(h1 ^ fingerprint.charCodeAt(i), 0x01000193) >>> 0;
    h2 = Math.imul(h2 + fingerprint.charCodeAt(i), 0x85ebca6b) >>> 0;
  }
  return (h1.toString(16).padStart(8, "0") + h2.toString(16).padStart(8, "0")).slice(0, 16);
}

const RUN_STATUSES = ["idle", "working", "blocked", "done", "unknown"];

/**
 * Project the herd into runs exactly as internal/state/runs.go does: an empty
 * shell pane is not a run, a pane with no live generation is not addressable and
 * so is not a run, an unrecognized status reads as `unknown`, and the list is
 * ordered by pane id.
 */
function projectRuns() {
  const runs = herd.panes
    .filter((p) => {
      const agent = herd.agents.find((a) => a.pane_id === p.pane_id);
      if (!p.agent && !agent) return false;
      return herd.generations[p.pane_id] !== undefined;
    })
    .map((p) => {
      const agent = herd.agents.find((a) => a.pane_id === p.pane_id);
      const workspace = herd.workspaces.find((w) => w.workspace_id === p.workspace_id);
      const tab = herd.tabs.find((t) => t.tab_id === p.tab_id);
      const worktree = workspace?.worktree;
      const generation = herd.generations[p.pane_id];
      const rawStatus = p.agent_status || agent?.agent_status || "";
      const status = RUN_STATUSES.includes(rawStatus) ? rawStatus : "unknown";
      // `omitempty` on the Go struct: an empty optional field is absent from the
      // wire, not present as "". The browser must cope with either.
      const optional = (key: string, value: string | undefined) => (value ? { [key]: value } : {});
      const agentIncarnation = incarnation(p.pane_id);
      return {
        // Mirrors internal/state/runs.go runID: pane, generation, AND occupant
        // digest, so a recycled pane id restarting at generation 1 cannot reuse a
        // dead run's identity. Opaque to the client either way.
        run_id: `${p.pane_id}@${generation}#${agentIncarnation}`,
        pane_id: p.pane_id,
        pane_generation: generation,
        agent_incarnation: agentIncarnation,
        workspace_id: p.workspace_id,
        ...optional("workspace_label", workspace?.label),
        tab_id: p.tab_id,
        ...optional("tab_label", tab?.label),
        terminal_id: p.terminal_id,
        agent_kind: p.agent || agent?.agent || "",
        ...optional("agent_name", agent?.name),
        ...optional("display_agent", p.display_agent),
        ...optional("title", p.title || agent?.terminal_title_stripped),
        status,
        interactive_ready: agent?.interactive_ready ?? false,
        launch_pending: false,
        focused: p.focused,
        ...optional("cwd", p.cwd),
        ...optional("foreground_cwd", p.foreground_cwd),
        ...(worktree
          ? {
              // internal/server/runs.go RunWorktree: the workspace's provenance
              // verbatim, minus repo_key. No branch — there is none to carry.
              worktree: {
                repo_name: worktree.repo_name,
                repo_root: worktree.repo_root,
                checkout_path: worktree.checkout_path,
                is_linked_worktree: worktree.is_linked_worktree,
              },
            }
          : {}),
        revision: p.revision,
        state_change_seq: agent?.state_change_seq ?? 0,
      };
    });
  runs.sort((a, b) => (a.pane_id < b.pane_id ? -1 : a.pane_id > b.pane_id ? 1 : 0));
  return runs;
}

function capabilities() {
  return {
    version: 1,
    operations: [
      "agent.focus", "agent.prompt", "agent.rename", "agent.send_keys", "agent.start",
      "pane.close", "pane.focus", "pane.move", "pane.rename", "pane.resize", "pane.split", "pane.swap", "pane.zoom",
      "tab.close", "tab.create", "tab.focus", "tab.move", "tab.rename",
      "workspace.close", "workspace.create", "workspace.focus", "workspace.rename",
      "worktree.create", "worktree.open", "worktree.remove", "worktree.remove_force",
    ],
    capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds: ["claude", "codex", "opencode", "gemini", "cursor"] },
    status: { version: "0.4.0", protocol: 17, mode, ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: eventClients.size },
    tunnel: {
      mode,
      public_url: mode === "named" ? "https://phone.example.com" : "https://example.trycloudflare.com",
      health: { healthy: true, detail: "ready" },
    },
    limits: {
      max_body_bytes: 1048576,
      max_pane_read_lines: 5000,
      confirmation_ttl_seconds: 30,
      ...(runContract.supported
        ? {
            max_run_output_lines: runContract.maxOutputLines,
            max_run_output_bytes: runContract.maxOutputBytes,
            max_runs: runContract.maxRuns,
          }
        : {}),
    },
    // An older relay has no `runs` document at all, and the browser must fail
    // closed to snapshot + pane.read when it is absent.
    ...(runContract.supported ? { runs: runCapabilities() } : {}),
  };
}

// Test-only outage switch: the events socket closes, refuses reconnects, and
// /snapshot returns 503 — a REAL disconnect, not a flag that leaves the socket
// OPEN, so e2e can exercise the readyState-based health logic deterministically.
let outage = false;

/**
 * Test-only fault injection. `failNext` makes the named operation fail once with
 * a chosen status/code; `uncertainNext` makes it fail once as a *retryable*
 * error, which is how the relay reports "Herdr may or may not have acted".
 */
const failNext = new Map<string, { status: number; code: string; message: string; retryable: boolean }>();

const eventClients = new Set<WebSocket>();
function broadcast() {
  herd.seq += 1;
  const msg = JSON.stringify({ type: "snapshot", snapshot: envelope() });
  for (const ws of eventClients) if (ws.readyState === ws.OPEN) ws.send(msg);
}

/* ---------------------------------------------------------- the allowlist */

interface OpSpec {
  /** Canonical params field naming the target resource. */
  resourceField: "" | "workspace_id" | "tab_id" | "pane_id" | "worktree_id";
  /** An identifier the real dispatcher would prefer; a divergent value is refused. */
  altResourceField?: string;
  requiresConfirmation?: boolean;
  /** Exactly the fields internal/integration/mutate.go decodes for this op. */
  fields: string[];
}

/** Mirrors internal/server/mutations.go `operations` + mutate.go's param structs. */
const OPERATIONS: Record<string, OpSpec> = {
  "workspace.create": { resourceField: "", fields: ["cwd", "label", "env", "focus"] },
  "workspace.focus": { resourceField: "workspace_id", fields: ["workspace_id"] },
  "workspace.rename": { resourceField: "workspace_id", fields: ["workspace_id", "label"] },
  "workspace.close": { resourceField: "workspace_id", requiresConfirmation: true, fields: ["workspace_id"] },

  "tab.create": { resourceField: "", fields: ["workspace_id", "cwd", "label", "env", "focus"] },
  "tab.focus": { resourceField: "tab_id", fields: ["tab_id"] },
  "tab.rename": { resourceField: "tab_id", fields: ["tab_id", "label"] },
  "tab.move": { resourceField: "tab_id", fields: ["tab_id", "insert_index"] },
  "tab.close": { resourceField: "tab_id", requiresConfirmation: true, fields: ["tab_id"] },

  "pane.focus": { resourceField: "pane_id", fields: ["pane_id"] },
  "pane.split": { resourceField: "pane_id", fields: ["pane_id", "direction", "ratio", "cwd", "env", "focus"] },
  "pane.resize": { resourceField: "pane_id", fields: ["pane_id", "direction", "amount"] },
  "pane.zoom": { resourceField: "pane_id", fields: ["pane_id", "mode"] },
  "pane.swap": { resourceField: "pane_id", fields: ["pane_id", "target_pane_id"] },
  "pane.move": { resourceField: "pane_id", fields: ["pane_id", "focus", "destination"] },
  "pane.rename": { resourceField: "pane_id", fields: ["pane_id", "label"] },
  "pane.close": { resourceField: "pane_id", requiresConfirmation: true, fields: ["pane_id"] },

  // The dispatcher prefers `target` over `pane_id` when present, so a divergent
  // `target` is refused. mutate.go does not decode `target` at all, so any value
  // is also a strict-params violation — both guards are modelled.
  "agent.focus": { resourceField: "pane_id", altResourceField: "target", fields: ["pane_id"] },
  "agent.prompt": { resourceField: "pane_id", altResourceField: "target", fields: ["pane_id", "text"] },
  "agent.send_keys": { resourceField: "pane_id", altResourceField: "target", fields: ["pane_id", "keys"] },
  "agent.rename": { resourceField: "pane_id", altResourceField: "target", fields: ["pane_id", "name"] },
  "agent.start": { resourceField: "pane_id", fields: ["pane_id", "kind", "name", "args"] },

  "worktree.create": { resourceField: "", fields: ["workspace_id", "cwd", "branch", "base", "path", "label", "focus"] },
  "worktree.open": { resourceField: "", fields: ["workspace_id", "cwd", "branch", "path", "label", "focus"] },
  "worktree.remove": { resourceField: "worktree_id", altResourceField: "workspace_id", requiresConfirmation: true, fields: ["worktree_id"] },
  "worktree.remove_force": { resourceField: "worktree_id", altResourceField: "workspace_id", requiresConfirmation: true, fields: ["worktree_id"] },
};

/** terminal.takeover is confirmable but is not a mutation. */
const CONFIRMABLE: Record<string, { resourceField: string; altResourceField?: string }> = {
  "workspace.close": { resourceField: "workspace_id" },
  "tab.close": { resourceField: "tab_id" },
  "pane.close": { resourceField: "pane_id" },
  "worktree.remove": { resourceField: "worktree_id", altResourceField: "workspace_id" },
  "worktree.remove_force": { resourceField: "worktree_id", altResourceField: "workspace_id" },
  "terminal.takeover": { resourceField: "pane_id" },
};

const generationChecked = (resourceField: string) => resourceField === "pane_id";

function canonicalJSON(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value ?? null);
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`;
  const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonicalJSON(v)}`).join(",")}}`;
}

/* ------------------------------------------------------------- confirmations */

interface Nonce {
  operation: string;
  resource: string;
  generation: number;
  paramsHash: string;
  expires: number;
}
const nonces = new Map<string, Nonce>();

function issueNonce(operation: string, resource: string, generation: number, params: unknown) {
  const confirmation = `cnf-${operation}-${resource}-${now()}`;
  nonces.set(confirmation, {
    operation,
    resource,
    generation,
    paramsHash: canonicalJSON(params ?? {}),
    expires: Date.now() + 30_000,
  });
  return { confirmation, expires_unix_ms: Date.now() + 30_000 };
}

function consumeNonce(token: string, operation: string, resource: string, generation: number, params: unknown): boolean {
  const nonce = nonces.get(token);
  if (!nonce) return false;
  const ok =
    nonce.operation === operation &&
    nonce.resource === resource &&
    nonce.generation === generation &&
    nonce.paramsHash === canonicalJSON(params ?? {}) &&
    nonce.expires >= Date.now();
  // Single use either way: a mismatched attempt burns the token, exactly as the
  // server's consume-on-attempt semantics do.
  nonces.delete(token);
  return ok;
}

/* ---------------------------------------------------------------- mutations */

const idempotency = new Map<string, unknown>();

function errPayload(requestId: string, code: string, message: string, retryable = false) {
  return { request_id: requestId, error: { code, message, retryable } };
}

function newPaneId(workspaceId: string) {
  const id = `${workspaceId}:p${herd.idSeq++}`;
  herd.generations[id] = 1;
  return id;
}

function applyMutation(body: Record<string, unknown>): { status: number; payload: unknown } {
  const requestId = String(body.request_id ?? "");
  if (!requestId) return { status: 400, payload: errPayload("", "bad_request", "missing request id") };

  const op = String(body.operation ?? "");
  const spec = OPERATIONS[op];
  if (!spec) return { status: 400, payload: errPayload(requestId, "bad_request", "unknown operation") };

  if (idempotency.has(requestId)) return { status: 200, payload: idempotency.get(requestId) };

  const params = (body.params ?? {}) as Record<string, unknown>;
  const expectedGeneration = Number(body.expected_generation ?? 0);
  const confirmation = body.confirmation ? String(body.confirmation) : "";

  const resource = spec.resourceField ? String(params[spec.resourceField] ?? "") : "";

  // A divergent alternate identifier would let the guard and the dispatch key on
  // different resources. Checked before the strict decode, as the server does.
  if (spec.altResourceField) {
    const alt = params[spec.altResourceField];
    if (typeof alt === "string" && alt && alt !== resource) {
      return { status: 400, payload: errPayload(requestId, "bad_request", "conflicting resource identifiers") };
    }
  }

  // Strict params: Go decodes each operation into its own struct with
  // DisallowUnknownFields, so an extra key is invalid_params, not ignored.
  for (const key of Object.keys(params)) {
    if (!spec.fields.includes(key)) {
      return { status: 400, payload: errPayload(requestId, "invalid_params", `unexpected field ${key}`) };
    }
  }

  if (spec.requiresConfirmation && !confirmation) {
    return { status: 428, payload: errPayload(requestId, "confirmation_required", "confirmation required") };
  }

  // Mandatory generation guard. Live generations start at 1, so a missing or
  // zero value can never match and must be refused outright.
  if (generationChecked(spec.resourceField)) {
    if (!Number.isInteger(expectedGeneration) || expectedGeneration <= 0) {
      return {
        status: 400,
        payload: errPayload(requestId, "generation_stale", "expected_generation is required for this operation"),
      };
    }
    const current = herd.generations[resource];
    if (current === undefined) {
      return { status: 409, payload: errPayload(requestId, "generation_stale", "resource no longer exists", true) };
    }
    if (current !== expectedGeneration) {
      return { status: 409, payload: errPayload(requestId, "generation_stale", "resource changed; refresh and retry", true) };
    }
  }

  if (spec.requiresConfirmation && !consumeNonce(confirmation, op, resource, expectedGeneration, params)) {
    return { status: 403, payload: errPayload(requestId, "confirmation_invalid", "confirmation invalid or expired") };
  }

  // Test-only fault injection, applied after every real guard so an injected
  // failure never masks a contract violation.
  const injected = failNext.get(op);
  if (injected) {
    failNext.delete(op);
    return {
      status: injected.status,
      payload: errPayload(requestId, injected.code, injected.message, injected.retryable),
    };
  }

  const outcome = dispatch(op, params, requestId);
  if ("status" in outcome) return outcome;

  const payload = { request_id: requestId, accepted: true, result: outcome.result };
  idempotency.set(requestId, payload);
  broadcast();
  return { status: 200, payload };
}

type Dispatched = { result: Record<string, unknown> } | { status: number; payload: unknown };

function notFound(requestId: string, kind: string): Dispatched {
  return { status: 404, payload: errPayload(requestId, "not_found", `${kind} not found`) };
}

function dispatch(op: string, params: Record<string, unknown>, requestId: string): Dispatched {
  switch (op) {
    case "workspace.create": {
      const n = herd.workspaces.length + 1;
      const id = `w${herd.idSeq++}`;
      const tabId = `${id}:t1`;
      const paneId = newPaneId(id);
      const cwd = String(params.cwd || "/Users/dev/code");
      herd.workspaces.push({ workspace_id: id, number: n, label: String(params.label || `space-${n}`), focused: false, pane_count: 1, tab_count: 1, active_tab_id: tabId, agent_status: "idle" });
      herd.tabs.push({ tab_id: tabId, workspace_id: id, number: 1, label: "shell", focused: false, pane_count: 1, agent_status: "idle" });
      herd.panes.push({ pane_id: paneId, terminal_id: `term_${herd.idSeq}`, workspace_id: id, tab_id: tabId, focused: false, cwd, foreground_cwd: cwd, title: "zsh", revision: 0 });
      return { result: { workspace: { workspace_id: id }, tab: { tab_id: tabId }, root_pane: { pane_id: paneId } } };
    }
    case "tab.create": {
      const wsId = String(params.workspace_id ?? "");
      const ws = herd.workspaces.find((w) => w.workspace_id === wsId);
      if (!ws) return notFound(requestId, "workspace");
      const order = herd.tabs.filter((t) => t.workspace_id === wsId).length;
      const id = `${wsId}:t${herd.idSeq++}`;
      const paneId = newPaneId(wsId);
      herd.tabs.push({ tab_id: id, workspace_id: wsId, number: order + 1, label: String(params.label || `tab-${order + 1}`), focused: false, pane_count: 1, agent_status: "idle" });
      herd.panes.push({ pane_id: paneId, terminal_id: `term_${herd.idSeq}`, workspace_id: wsId, tab_id: id, focused: false, cwd: "/Users/dev/code", foreground_cwd: "/Users/dev/code", title: "zsh", revision: 0 });
      ws.tab_count += 1;
      return { result: { tab: { tab_id: id }, root_pane: { pane_id: paneId } } };
    }
    case "pane.split": {
      const paneId = String(params.pane_id);
      const pane = herd.panes.find((p) => p.pane_id === paneId);
      if (!pane) return notFound(requestId, "pane");
      const id = newPaneId(pane.workspace_id);
      herd.panes.push({ pane_id: id, terminal_id: `term_${herd.idSeq}`, workspace_id: pane.workspace_id, tab_id: pane.tab_id, focused: false, cwd: pane.cwd, foreground_cwd: pane.cwd, title: "zsh", revision: 0 });
      const tab = herd.tabs.find((t) => t.tab_id === pane.tab_id);
      if (tab) tab.pane_count += 1;
      return { result: { pane: { pane_id: id } } };
    }
    case "workspace.focus":
      herd.workspaces.forEach((w) => (w.focused = w.workspace_id === params.workspace_id));
      herd.focusedWorkspaceId = String(params.workspace_id);
      return { result: { ok: true } };
    case "tab.focus":
      herd.focusedTabId = String(params.tab_id);
      return { result: { ok: true } };
    case "pane.focus":
      herd.focusedPaneId = String(params.pane_id);
      return { result: { ok: true } };
    case "workspace.rename": {
      const w = herd.workspaces.find((x) => x.workspace_id === params.workspace_id);
      if (w) w.label = String(params.label);
      return { result: { ok: true } };
    }
    case "tab.rename": {
      const t = herd.tabs.find((x) => x.tab_id === params.tab_id);
      if (t) t.label = String(params.label);
      return { result: { tab: t ?? null } as Record<string, unknown> };
    }
    case "pane.rename": {
      const p = herd.panes.find((x) => x.pane_id === params.pane_id);
      if (p) p.title = String(params.label);
      return { result: { ok: true } };
    }
    case "tab.move": {
      const t = herd.tabs.find((x) => x.tab_id === params.tab_id);
      if (!t) return notFound(requestId, "tab");
      const wsTabs = herd.tabs.filter((x) => x.workspace_id === t.workspace_id);
      const from = wsTabs.findIndex((x) => x.tab_id === t.tab_id);
      const insert = Number(params.insert_index ?? from);
      const without = wsTabs.filter((x) => x.tab_id !== t.tab_id);
      // insert_index counts the pre-removal list (Herdr semantics).
      let at = insert > from ? insert - 1 : insert;
      at = Math.max(0, Math.min(without.length, at));
      without.splice(at, 0, t);
      let wi = 0;
      herd.tabs = herd.tabs.map((x) => (x.workspace_id === t.workspace_id ? without[wi++] : x));
      return { result: { tabs: without } };
    }
    case "pane.zoom": {
      const p = herd.panes.find((x) => x.pane_id === params.pane_id);
      const layout = herd.layouts.find((l) => l.tab_id === p?.tab_id);
      if (layout) layout.zoomed = !layout.zoomed;
      return { result: { ok: true } };
    }
    case "pane.resize":
    case "pane.swap":
    case "agent.focus":
    case "worktree.open":
      return { result: { ok: true } };
    case "agent.send_keys": {
      const agent = herd.agents.find((a) => a.pane_id === params.pane_id);
      if (!agent) return notFound(requestId, "agent");
      return { result: { ok: true } };
    }
    case "pane.move": {
      const pane = herd.panes.find((p) => p.pane_id === params.pane_id);
      const dest = (params.destination ?? {}) as { type?: string; tab_id?: string };
      if (pane && dest.type === "tab" && dest.tab_id) {
        const oldTab = herd.tabs.find((t) => t.tab_id === pane.tab_id);
        const newTab = herd.tabs.find((t) => t.tab_id === dest.tab_id);
        if (newTab) {
          if (oldTab) oldTab.pane_count = Math.max(0, oldTab.pane_count - 1);
          pane.tab_id = newTab.tab_id;
          newTab.pane_count += 1;
        }
      }
      return { result: { ok: true } };
    }
    case "workspace.close":
      herd.workspaces = herd.workspaces.filter((w) => w.workspace_id !== params.workspace_id);
      herd.tabs = herd.tabs.filter((t) => t.workspace_id !== params.workspace_id);
      for (const p of herd.panes.filter((p) => p.workspace_id === params.workspace_id)) delete herd.generations[p.pane_id];
      herd.panes = herd.panes.filter((p) => p.workspace_id !== params.workspace_id);
      herd.agents = herd.agents.filter((a) => a.workspace_id !== params.workspace_id);
      return { result: { ok: true } };
    case "tab.close":
      herd.tabs = herd.tabs.filter((t) => t.tab_id !== params.tab_id);
      for (const p of herd.panes.filter((p) => p.tab_id === params.tab_id)) delete herd.generations[p.pane_id];
      herd.panes = herd.panes.filter((p) => p.tab_id !== params.tab_id);
      herd.agents = herd.agents.filter((a) => a.tab_id !== params.tab_id);
      return { result: { ok: true } };
    case "pane.close":
      herd.panes = herd.panes.filter((p) => p.pane_id !== params.pane_id);
      herd.agents = herd.agents.filter((a) => a.pane_id !== params.pane_id);
      delete herd.generations[String(params.pane_id)];
      return { result: { ok: true } };
    case "agent.prompt": {
      const agent = herd.agents.find((a) => a.pane_id === params.pane_id);
      if (!agent) return notFound(requestId, "agent");
      agent.agent_status = "working";
      agent.state_change_seq = now();
      const pane = herd.panes.find((p) => p.pane_id === agent.pane_id);
      if (pane) pane.agent_status = "working";
      return { result: { ok: true } };
    }
    case "agent.rename": {
      const agent = herd.agents.find((a) => a.pane_id === params.pane_id);
      if (!agent) return notFound(requestId, "agent");
      const name = String(params.name ?? "");
      if (!/^[a-z][a-z0-9_-]{0,31}$/.test(name)) {
        return { status: 400, payload: errPayload(requestId, "invalid_params", "invalid agent name") };
      }
      agent.name = name;
      const pane = herd.panes.find((p) => p.pane_id === agent.pane_id);
      if (pane) pane.display_agent = name;
      return { result: { agent: { name } } };
    }
    case "agent.start": {
      const paneId = String(params.pane_id);
      const kind = String(params.kind ?? "");
      const name = String(params.name ?? "");
      // Mirror internal/herdr/agents.go ValidAgentName + uniqueness.
      if (!/^[a-z][a-z0-9_-]{0,31}$/.test(name)) {
        return { status: 400, payload: errPayload(requestId, "invalid_params", "invalid agent name") };
      }
      if (herd.agents.some((a) => a.name === name)) {
        return { status: 409, payload: errPayload(requestId, "conflict", "agent name in use") };
      }
      const pane = herd.panes.find((p) => p.pane_id === paneId);
      if (!pane) return notFound(requestId, "pane");
      pane.agent = kind;
      pane.display_agent = name;
      pane.agent_status = "working";
      herd.agents.push({
        terminal_id: pane.terminal_id, agent: kind, name, agent_status: "working",
        workspace_id: pane.workspace_id, tab_id: pane.tab_id, pane_id: paneId, focused: false,
        interactive_ready: true, state_change_seq: now(), cwd: pane.cwd, foreground_cwd: pane.cwd,
        terminal_title_stripped: `${name} starting`, revision: 0,
      });
      return { result: { agent: { name, agent: kind } } };
    }
    case "worktree.create": {
      // Herdr creates the worktree AND opens it as a workspace with its first
      // tab and root pane, so the result carries all four.
      const branch = String(params.branch || "feature");
      const path = String(params.path || `${String(params.cwd || "/Users/dev/code")}/${branch}`);
      const id = `w${herd.idSeq++}`;
      const tabId = `${id}:t1`;
      const paneId = newPaneId(id);
      const repoRoot = String(params.cwd || "/Users/dev/code");
      herd.workspaces.push({
        workspace_id: id, number: herd.workspaces.length + 1, label: String(params.label || branch),
        focused: false, pane_count: 1, tab_count: 1, active_tab_id: tabId, agent_status: "idle",
        // A created worktree is a linked checkout, so it is removable.
        worktree: worktreeInfo(repoRoot.split("/").filter(Boolean).pop() || branch, repoRoot, path, true),
      });
      herd.tabs.push({ tab_id: tabId, workspace_id: id, number: 1, label: "shell", focused: false, pane_count: 1, agent_status: "idle" });
      herd.panes.push({ pane_id: paneId, terminal_id: `term_${herd.idSeq}`, workspace_id: id, tab_id: tabId, focused: false, cwd: path, foreground_cwd: path, title: "zsh", revision: 0 });
      return {
        result: {
          worktree: { path, branch },
          workspace: { workspace_id: id },
          tab: { tab_id: tabId },
          root_pane: { pane_id: paneId },
        },
      };
    }
    case "worktree.remove":
    case "worktree.remove_force": {
      // Herdr removes the checkout AND closes the workspace it was open in, so
      // the provenance disappears with the workspace, not on its own.
      const workspaceId = String(params.worktree_id ?? "");
      herd.workspaces = herd.workspaces.filter((w) => w.workspace_id !== workspaceId);
      herd.tabs = herd.tabs.filter((t) => t.workspace_id !== workspaceId);
      const gone = herd.panes.filter((p) => p.workspace_id === workspaceId).map((p) => p.pane_id);
      herd.panes = herd.panes.filter((p) => p.workspace_id !== workspaceId);
      herd.agents = herd.agents.filter((a) => a.workspace_id !== workspaceId);
      for (const id of gone) delete herd.generations[id];
      return { result: { ok: true, workspace_id: workspaceId } };
    }
    default:
      return { status: 400, payload: errPayload(requestId, "bad_request", `unknown operation ${op}`) };
  }
}

/* --------------------------------------------------------------- http utils */
function send(res: ServerResponse, status: number, body: unknown, headers: Record<string, string> = {}) {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", ...headers });
  res.end(JSON.stringify(body));
}
function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (c) => (data += c));
    req.on("end", () => {
      try {
        resolve(data ? (JSON.parse(data) as Record<string, unknown>) : {});
      } catch {
        resolve({});
      }
    });
  });
}
function hasSessionCookie(req: IncomingMessage): boolean {
  return (req.headers.cookie ?? "").includes(`${COOKIE}=`);
}

/** internal/server/errors.go writeError, for the two 401s the middleware emits. */
function unauthorized(res: ServerResponse, message: string): void {
  send(res, 401, { error: { code: "unauthorized", message, retryable: false } });
}

/**
 * The app-session gate, mirroring `internal/server/routes.go` `wrap` steps 2–3.
 * Returns false when it has already answered the request.
 *
 * Named mode auto-provisions rather than sending the browser to `/pair`: step 2
 * re-validated the Access JWT at the origin, so a cookie-less request still
 * carries a verified edge identity. Quick mode has none, so it falls straight
 * through to 401 and pairing stays the only way in.
 */
function authorize(req: IncomingMessage, res: ServerResponse): boolean {
  if (mode === "named") {
    if (accessDenied) {
      unauthorized(res, "access denied");
      return false;
    }
    if (!hasSessionCookie(req)) {
      // Set before send(), which merges pre-set headers with its own.
      res.setHeader("Set-Cookie", `${COOKIE}=1; Path=/; HttpOnly; SameSite=Strict`);
    }
    return true;
  }
  if (!hasSessionCookie(req)) {
    unauthorized(res, "no valid session");
    return false;
  }
  return true;
}

function handleConfirmations(res: ServerResponse, body: Record<string, unknown>): void {
  const operation = String(body.operation ?? "");
  const spec = CONFIRMABLE[operation];
  if (!spec) {
    send(res, 400, { error: { code: "bad_request", message: "operation does not require confirmation" } });
    return;
  }
  const resourceId = String(body.resource_id ?? "");
  const params = (body.params ?? {}) as Record<string, unknown>;
  const expectedGeneration = Number(body.expected_generation ?? 0);

  if (spec.resourceField && !resourceId) {
    send(res, 400, { error: { code: "bad_request", message: "missing resource id" } });
    return;
  }
  const inParams = spec.resourceField ? params[spec.resourceField] : undefined;
  if (typeof inParams === "string" && inParams && inParams !== resourceId) {
    send(res, 400, { error: { code: "bad_request", message: "resource id mismatch" } });
    return;
  }
  if (spec.altResourceField) {
    const alt = params[spec.altResourceField];
    if (typeof alt === "string" && alt && alt !== resourceId) {
      send(res, 400, { error: { code: "bad_request", message: "conflicting resource identifiers" } });
      return;
    }
  }
  if (generationChecked(spec.resourceField)) {
    const current = herd.generations[resourceId];
    if (current === undefined) {
      send(res, 404, { error: { code: "not_found", message: "resource not found" } });
      return;
    }
    if (current !== expectedGeneration) {
      send(res, 409, { error: { code: "generation_stale", message: "resource changed; refresh and retry" } });
      return;
    }
  }
  send(res, 200, issueNonce(operation, resourceId, expectedGeneration, params));
}

/** The bytes a pane last rendered. The same text feeds both read surfaces. */
function paneOutput(paneId: string): string {
  const agent = herd.agents.find((a) => a.pane_id === paneId);
  if (!agent) return `$ cd ${paneId}\n$ `;
  return `$ ${agent.agent} --resume\n${agent.terminal_title_stripped || "working"}\n· reading src/server/reconnect.ts\n· editing src/server/reconnect.ts\n${"·".repeat(runOutputPadding)}\n$ `;
}

/** Test-only: pad observed output so the byte bound (and truncation) applies. */
let runOutputPadding = 0;

/** Test-only: make the next observed-output read fail once with a given code. */
let failNextRunRead: { status: number; code: string; message: string } | null = null;

/** Byte-bound the tail, cut on a UTF-8 boundary, exactly as the relay does. */
function boundObservedText(text: string, maxBytes: number): { text: string; truncated: boolean } {
  const buf = Buffer.from(text, "utf8");
  if (maxBytes <= 0 || buf.length <= maxBytes) return { text, truncated: false };
  let tail = buf.subarray(buf.length - maxBytes);
  // Advance past a partial leading rune so the result is always valid UTF-8.
  let at = 0;
  while (at < tail.length && (tail[at] & 0xc0) === 0x80) at++;
  tail = tail.subarray(at);
  return { text: tail.toString("utf8"), truncated: true };
}

/** internal/server/runs.go countLines: empty text is zero lines; a trailing
 * newline terminates the last line rather than starting a new one. */
function countLines(text: string): number {
  if (text === "") return 0;
  let n = 0;
  for (const ch of text) if (ch === "\n") n++;
  return text.endsWith("\n") ? n : n + 1;
}

function runError(res: ServerResponse, status: number, code: string, message: string) {
  // internal/server/errors.go writeError: static message, no retryable flag.
  send(res, status, { error: { code, message } }, { "Cache-Control": "no-store" });
}

/**
 * `GET /runs/{pane_id}` — the same guard order as internal/server/runs.go:
 * mandatory nonzero generation, then source/lines validation, then the
 * generation check *before* any read, then the run lookup, then the read.
 */
function handleRunDetail(res: ServerResponse, paneId: string, url: URL): void {
  if (!paneId) {
    runError(res, 400, "bad_request", "missing pane id");
    return;
  }
  const raw = url.searchParams.get("expected_generation");
  const expected = raw === null ? NaN : Number(raw);
  if (raw === null || raw === "" || !Number.isInteger(expected) || expected <= 0) {
    runError(res, 400, "generation_stale", "expected_generation is required to read a run");
    return;
  }
  const source = url.searchParams.get("source") || "recent-unwrapped";
  if (!RUN_OUTPUT_SOURCES.includes(source)) {
    runError(res, 400, "bad_request", "invalid source");
    return;
  }
  let lines = DEFAULT_RUN_OUTPUT_LINES;
  const rawLines = url.searchParams.get("lines");
  if (rawLines) {
    const n = Number(rawLines);
    if (!Number.isInteger(n) || n <= 0) {
      runError(res, 400, "bad_request", "invalid lines");
      return;
    }
    lines = n;
  }
  if (lines > runContract.maxOutputLines) lines = runContract.maxOutputLines;

  const current = herd.generations[paneId];
  if (current === undefined) {
    runError(res, 409, "generation_stale", "pane no longer exists");
    return;
  }
  if (current !== expected) {
    runError(res, 409, "generation_stale", "pane changed; refresh and retry");
    return;
  }

  const run = projectRuns().find((r) => r.pane_id === paneId);
  if (!run) {
    runError(res, 404, "run_unavailable", "no agent run occupies this pane");
    return;
  }

  if (failNextRunRead) {
    const injected = failNextRunRead;
    failNextRunRead = null;
    runError(res, injected.status, injected.code, injected.message);
    return;
  }

  const bounded = boundObservedText(paneOutput(paneId), runContract.maxOutputBytes);
  send(
    res,
    200,
    {
      contract_version: RUN_CONTRACT_VERSION,
      capabilities: runCapabilities(),
      run,
      parts: [
        {
          type: PART_OBSERVED_TERMINAL_OUTPUT,
          source,
          format: "text",
          // internal/server/runs.go countLines: how many lines came back, NOT the
          // clamped request. The UI states this as fact, so it must be true.
          lines: countLines(bounded.text),
          bytes: Buffer.byteLength(bounded.text, "utf8"),
          truncated: bounded.truncated,
          text: bounded.text,
        },
        // Additive: the observed part above is emitted identically either way, so
        // the raw tail never disappears when interpretation is on.
        ...interpretedParts(paneId),
      ],
    },
    { "Cache-Control": "no-store" },
  );
}

async function handleApi(req: IncomingMessage, res: ServerResponse, url: URL): Promise<boolean> {
  const path = url.pathname.replace("/api/v1", "");
  const method = req.method ?? "GET";

  if (path === "/health") {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok");
    return true;
  }
  if (path === "/__reset") {
    herd = seed();
    nonces.clear();
    idempotency.clear();
    terminalOwners.clear();
    failNext.clear();
    outage = false;
    runContract.supported = true;
    runContract.maxRuns = 200;
    runOutputPadding = 0;
    failNextRunRead = null;
    // Back to off, matching the config default: one journey enabling the
    // experimental feature must not leak into the next.
    interpretation.enabled = false;
    interpretation.parsers = ["claude", "opencode"];
    // Back to the mode this server was started in, so one journey's mode switch
    // cannot leak into the next.
    mode = ENV_MODE;
    accessDenied = false;
    send(res, 200, { ok: true, mode });
    return true;
  }
  if (path === "/__mode" && method === "POST") {
    // Test-only: switch the emulated relay mode, and/or model an expired Access
    // token in named mode (every authenticated route then 401s `access denied`).
    const body = await readBody(req);
    if (body.mode !== undefined) mode = body.mode === "named" ? "named" : "quick";
    if (body.access_denied !== undefined) accessDenied = !!body.access_denied;
    send(res, 200, { mode, access_denied: accessDenied });
    return true;
  }
  if (path === "/__interpretation" && method === "POST") {
    // Test-only: emulate `[experimental] agent_output_parsing` (SPEC §12.2).
    // Off by default; `{"enabled":true}` advertises the capability and appends the
    // interpreted parts, `{"parsers":["claude"]}` narrows which agents are parsed.
    const body = await readBody(req);
    if (body.enabled !== undefined) interpretation.enabled = !!body.enabled;
    if (Array.isArray(body.parsers)) interpretation.parsers = body.parsers.map(String);
    broadcast();
    send(res, 200, { enabled: interpretation.enabled, parsers: interpretation.parsers });
    return true;
  }
  if (path === "/__run_contract" && method === "POST") {
    // Test-only: model an OLDER relay (`supported: false` removes the `runs`
    // capability document and both run routes), lower the list bound to
    // exercise truncation, or pad observed output past the byte bound.
    const body = await readBody(req);
    if (body.supported !== undefined) runContract.supported = !!body.supported;
    if (body.max_runs !== undefined) runContract.maxRuns = Number(body.max_runs);
    if (body.output_padding !== undefined) runOutputPadding = Number(body.output_padding);
    broadcast();
    send(res, 200, { supported: runContract.supported, max_runs: runContract.maxRuns });
    return true;
  }
  if (path === "/__fail_next_run_read" && method === "POST") {
    // Test-only: the next observed-output read fails once with a stable code.
    const body = await readBody(req);
    failNextRunRead = {
      status: Number(body.status ?? 502),
      code: String(body.code ?? "run_read_failed"),
      message: String(body.message ?? "run output unavailable"),
    };
    send(res, 200, { ok: true });
    return true;
  }
  if (path === "/__outage" && method === "POST") {
    const body = await readBody(req);
    outage = !!body.on;
    if (outage) {
      for (const ws of eventClients) {
        try {
          ws.close();
        } catch {
          /* already closing */
        }
      }
    }
    send(res, 200, { outage });
    return true;
  }
  if (path === "/__fail_next" && method === "POST") {
    // Test-only: make the next call to `operation` fail once. `retryable: true`
    // is how the relay reports an uncertain outcome, which the UI must surface
    // as delivery-unknown rather than retrying.
    const body = await readBody(req);
    failNext.set(String(body.operation), {
      status: Number(body.status ?? 502),
      code: String(body.code ?? "internal"),
      message: String(body.message ?? "operation failed"),
      retryable: !!body.retryable,
    });
    send(res, 200, { ok: true });
    return true;
  }
  if (path === "/__replace_pane" && method === "POST") {
    // Test-only: recycle a pane the way Herdr does — same id, new generation.
    const body = await readBody(req);
    const paneId = String(body.pane_id ?? "");
    if (herd.generations[paneId] === undefined) {
      send(res, 404, { error: { code: "not_found", message: "pane not found" } });
      return true;
    }
    herd.generations[paneId] += 1;
    herd.agents = herd.agents.filter((a) => a.pane_id !== paneId);
    const pane = herd.panes.find((p) => p.pane_id === paneId);
    if (pane) {
      pane.agent = undefined;
      pane.display_agent = undefined;
      pane.agent_status = undefined;
      pane.title = "zsh";
    }
    broadcast();
    send(res, 200, { pane_id: paneId, generation: herd.generations[paneId] });
    return true;
  }
  if (path === "/pair" && method === "POST") {
    // `/pair` stays live in both modes (it is how a named-mode operator re-binds),
    // and in named mode it is still behind the Access check.
    if (mode === "named" && accessDenied) {
      unauthorized(res, "access denied");
      return true;
    }
    const body = await readBody(req);
    if (String(body.secret) !== PAIR_SECRET) {
      send(res, 401, { error: { code: "unauthorized", message: "pairing rejected", retryable: false } });
      return true;
    }
    send(res, 200, sessionPayload(), { "Set-Cookie": `${COOKIE}=1; Path=/; HttpOnly; SameSite=Strict` });
    return true;
  }
  if (path === "/session" && method === "GET") {
    if (!authorize(req, res)) return true;
    send(res, 200, sessionPayload());
    return true;
  }
  if (path === "/session" && method === "DELETE") {
    res.writeHead(204, { "Set-Cookie": `${COOKIE}=; Path=/; Max-Age=0` });
    res.end();
    return true;
  }
  if (path === "/capabilities" && method === "GET") {
    if (!authorize(req, res)) return true;
    send(res, 200, capabilities());
    return true;
  }
  if (path === "/snapshot" && method === "GET") {
    if (!authorize(req, res)) return true;
    if (outage) {
      send(res, 503, { error: { code: "unavailable", message: "relay outage", retryable: true } });
      return true;
    }
    const etag = `"h${herd.seq}"`;
    if (req.headers["if-none-match"] === etag) {
      res.writeHead(304, { ETag: etag, "Cache-Control": "no-cache" });
      res.end();
      return true;
    }
    send(res, 200, envelope(), { ETag: etag, "Cache-Control": "no-cache" });
    return true;
  }
  if (path.startsWith("/panes/") && path.endsWith("/read") && method === "GET") {
    if (!authorize(req, res)) return true;
    const paneId = decodeURIComponent(path.slice("/panes/".length, -"/read".length));
    if (herd.generations[paneId] === undefined) {
      send(res, 404, { error: { code: "not_found", message: "pane not found", retryable: false } });
      return true;
    }
    const lines = Number(url.searchParams.get("lines") ?? 100);
    send(res, 200, {
      pane_id: paneId,
      source: url.searchParams.get("source") ?? "visible",
      lines,
      content: paneOutput(paneId),
    });
    return true;
  }
  if (path === "/runs" && method === "GET") {
    if (!authorize(req, res)) return true;
    if (!runContract.supported) {
      // An older relay has no run routes at all.
      send(res, 404, { error: { code: "not_found", message: "unknown endpoint" } });
      return true;
    }
    const all = projectRuns();
    const truncated = all.length > runContract.maxRuns;
    send(
      res,
      200,
      {
        contract_version: RUN_CONTRACT_VERSION,
        capabilities: runCapabilities(),
        snapshot_hash: `h${herd.seq}`,
        runs: truncated ? all.slice(0, runContract.maxRuns) : all,
        truncated,
      },
      { "Cache-Control": "no-store" },
    );
    return true;
  }
  if (path.startsWith("/runs/") && method === "GET") {
    if (!authorize(req, res)) return true;
    if (!runContract.supported) {
      send(res, 404, { error: { code: "not_found", message: "unknown endpoint" } });
      return true;
    }
    handleRunDetail(res, decodeURIComponent(path.slice("/runs/".length)), url);
    return true;
  }
  if (path === "/directories" && method === "GET") {
    if (!authorize(req, res)) return true;
    const p = url.searchParams.get("path") ?? "/Users/dev/code";
    send(res, 200, { path: p, entries: [{ name: "space-api", path: `${p}/space-api` }, { name: "mobile-ui", path: `${p}/mobile-ui` }, { name: "infra", path: `${p}/infra` }] });
    return true;
  }
  if (path === "/confirmations" && method === "POST") {
    if (!authorize(req, res)) return true;
    handleConfirmations(res, await readBody(req));
    return true;
  }
  if (path === "/mutations" && method === "POST") {
    if (!authorize(req, res)) return true;
    const { status, payload } = applyMutation(await readBody(req));
    send(res, status, payload);
    return true;
  }
  return false;
}

/* ---------------------------------------------------------------- websockets */
const eventsWss = new WebSocketServer({ noServer: true });
const terminalWss = new WebSocketServer({ noServer: true });
const terminalOwners = new Map<string, WebSocket>();

eventsWss.on("connection", (ws) => {
  eventClients.add(ws);
  ws.send(JSON.stringify({ type: "snapshot", snapshot: envelope() }));
  ws.on("close", () => eventClients.delete(ws));
  ws.on("message", () => {
    /* the browser only sends control frames; nothing to act on */
  });
});

terminalWss.on("connection", (ws, req) => {
  const url = new URL(req.url ?? "", "http://localhost");
  const paneId = decodeURIComponent(url.pathname.split("/terminals/")[1] ?? "");
  const confirmation = url.searchParams.get("confirmation") ?? "";
  const expectedGeneration = Number(url.searchParams.get("expected_generation") ?? 0);

  // Takeover is honored only with a valid, generation-bound terminal.takeover
  // nonce (mirrors internal/server/terminalroute.go).
  const takeover =
    url.searchParams.get("takeover") === "1" &&
    consumeNonce(confirmation, "terminal.takeover", paneId, expectedGeneration, {});

  const existing = terminalOwners.get(paneId);
  const conflict = existing && existing.readyState === existing.OPEN && !takeover;

  if (conflict) {
    ws.send(JSON.stringify({ type: "terminal.conflict", reason: "another controller owns this pane" }));
  } else {
    if (existing && existing.readyState === existing.OPEN) existing.close();
    terminalOwners.set(paneId, ws);
    ws.send(JSON.stringify({ type: "terminal.opened", width: 80, height: 24, full: true }));
    ws.send(Buffer.from(`\r\n\x1b[38;2;80;168;163mherdr\x1b[0m:${paneId} $ `, "utf8"));
  }

  ws.on("message", (data, isBinary) => {
    if (isBinary) {
      // Echo input as a frame. Strip bracketed-paste markers a real app would
      // consume, and render a lone CR as CRLF so multi-line pastes show on
      // separate rows in the mirror.
      let s = Buffer.from(data as Buffer).toString("utf8");
      // eslint-disable-next-line no-control-regex
      s = s.replace(/\x1b\[20[01]~/g, "").replace(/\r(?!\n)/g, "\r\n");
      ws.send(Buffer.from(s, "utf8"));
      return;
    }
    try {
      const msg = JSON.parse(String(data)) as { type: string };
      if (msg.type === "ping") ws.send(JSON.stringify({ type: "terminal.pong" }));
      else if (msg.type === "release") {
        terminalOwners.delete(paneId);
        ws.send(JSON.stringify({ type: "terminal.closed", reason: "released" }));
      } else if (msg.type === "resize") ws.send(JSON.stringify({ type: "terminal.resized", width: 80, height: 24 }));
    } catch {
      /* ignore */
    }
  });
  ws.on("close", () => {
    if (terminalOwners.get(paneId) === ws) terminalOwners.delete(paneId);
  });
});

function handleUpgrade(req: IncomingMessage, socket: Duplex, head: Buffer) {
  const url = new URL(req.url ?? "", "http://localhost");
  // A WebSocket handshake is gated exactly like a request: named mode needs a
  // valid edge identity (the session rides along, provisioned or paired), quick
  // mode needs the paired cookie. No cookie can be set on an upgrade, so a
  // named-mode socket relies on the SPA having read GET /session first — which it
  // always does before opening /events.
  if (mode === "named" ? accessDenied : !hasSessionCookie(req)) {
    socket.destroy();
    return;
  }
  if (url.pathname === "/api/v1/events") {
    if (outage) {
      socket.destroy();
      return;
    }
    eventsWss.handleUpgrade(req, socket, head, (ws) => eventsWss.emit("connection", ws, req));
    return;
  }
  if (url.pathname.startsWith("/api/v1/terminals/")) {
    const paneId = decodeURIComponent(url.pathname.split("/terminals/")[1] ?? "");
    const expectedGeneration = Number(url.searchParams.get("expected_generation") ?? 0);
    // Attach is generation-checked and the assertion is mandatory: the server
    // answers 400/409 *before* the upgrade, which the browser observes as a
    // failed connection.
    const current = herd.generations[paneId];
    if (!Number.isInteger(expectedGeneration) || expectedGeneration <= 0 || current === undefined || current !== expectedGeneration) {
      socket.write("HTTP/1.1 409 Conflict\r\nConnection: close\r\n\r\n");
      socket.destroy();
      return;
    }
    terminalWss.handleUpgrade(req, socket, head, (ws) => terminalWss.emit("connection", ws, req));
    return;
  }
  socket.destroy();
}

function attach(middlewares: ViteDevServer["middlewares"], httpServer: ViteDevServer["httpServer"] | PreviewServer["httpServer"]) {
  middlewares.use((req, res, next) => {
    if (!req.url || !req.url.startsWith("/api/v1")) return next();
    const url = new URL(req.url, "http://localhost");
    void handleApi(req, res, url).then((handled) => {
      if (!handled) send(res, 404, { error: { code: "not_found", message: "unknown endpoint", retryable: false } });
    });
  });
  httpServer?.on("upgrade", handleUpgrade);
}

export function mockRelay(): Plugin {
  return {
    name: "herdr-phone-mock-relay",
    apply: () => true,
    configureServer(server) {
      attach(server.middlewares, server.httpServer);
    },
    configurePreviewServer(server) {
      attach(server.middlewares, server.httpServer);
    },
  };
}
