/**
 * Mock relay — dev + preview ONLY (SPEC §18 harness). A Vite plugin that stands
 * in for the Go relay so the real production bundle runs against a deterministic
 * in-memory herd. It emits the EXACT backend wire shapes (see internal/server/**,
 * internal/state/snapshot.go, internal/herdr/models.go, internal/terminal/
 * protocol.go): the nested snapshot envelope, capabilities document, pair/session
 * identity, `confirmation` nonces, mutation envelope, and the terminal control
 * message vocabulary. It is node-side only and never enters the browser bundle.
 */
import type { Plugin, ViteDevServer, PreviewServer } from "vite";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import { WebSocketServer, type WebSocket } from "ws";

const PAIR_SECRET = process.env.MOCK_PAIR_SECRET ?? "dev-pair-secret";
const COOKIE = "hp_mock_session";

let clock = 1_780_000_000_000;
const now = () => clock++;

interface WireWorkspace {
  workspace_id: string;
  number: number;
  label: string;
  focused: boolean;
  pane_count: number;
  tab_count: number;
  active_tab_id: string;
  agent_status: string;
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
interface WireWorktree {
  path: string;
  label: string;
  branch?: string;
  is_bare: boolean;
  is_detached: boolean;
  is_linked_worktree: boolean;
  is_prunable: boolean;
  open_workspace_id?: string;
}

interface Herd {
  seq: number;
  idSeq: number;
  workspaces: WireWorkspace[];
  tabs: WireTab[];
  panes: WirePane[];
  agents: WireAgent[];
  layouts: WireLayout[];
  worktrees: WireWorktree[];
  generations: Record<string, number>;
  focusedWorkspaceId: string;
  focusedTabId: string;
  focusedPaneId: string;
}

const FULL = { x: 0, y: 0, width: 1, height: 1 };

function seed(): Herd {
  const workspaces: WireWorkspace[] = [
    { workspace_id: "w1", number: 1, label: "space-api", focused: true, pane_count: 2, tab_count: 2, active_tab_id: "w1:t1", agent_status: "blocked" },
    { workspace_id: "w2", number: 2, label: "mobile-ui", focused: false, pane_count: 2, tab_count: 1, active_tab_id: "w2:t1", agent_status: "working" },
    { workspace_id: "w3", number: 3, label: "infra", focused: false, pane_count: 1, tab_count: 1, active_tab_id: "w3:t1", agent_status: "idle" },
  ];
  const tabs: WireTab[] = [
    { tab_id: "w1:t1", workspace_id: "w1", number: 1, label: "auth-refactor", focused: true, pane_count: 2, agent_status: "blocked" },
    { tab_id: "w1:t2", workspace_id: "w1", number: 2, label: "tests", focused: false, pane_count: 1, agent_status: "done" },
    { tab_id: "w2:t1", workspace_id: "w2", number: 1, label: "app", focused: false, pane_count: 2, agent_status: "working" },
    { tab_id: "w3:t1", workspace_id: "w3", number: 1, label: "shell", focused: false, pane_count: 1, agent_status: "idle" },
  ];
  const panes: WirePane[] = [
    { pane_id: "w1:p1", terminal_id: "term_1", workspace_id: "w1", tab_id: "w1:t1", focused: true, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", agent: "claude", display_agent: "claude", agent_status: "blocked", revision: 3 },
    { pane_id: "w1:p2", terminal_id: "term_2", workspace_id: "w1", tab_id: "w1:t1", focused: false, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", title: "server", revision: 1 },
    { pane_id: "w1:p3", terminal_id: "term_3", workspace_id: "w1", tab_id: "w1:t2", focused: false, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", agent: "codex", display_agent: "codex", agent_status: "done", revision: 2 },
    { pane_id: "w2:p1", terminal_id: "term_4", workspace_id: "w2", tab_id: "w2:t1", focused: false, cwd: "/Users/dev/code/mobile-ui", foreground_cwd: "/Users/dev/code/mobile-ui", agent: "opencode", display_agent: "opencode", agent_status: "working", revision: 5 },
    { pane_id: "w2:p2", terminal_id: "term_5", workspace_id: "w2", tab_id: "w2:t1", focused: false, cwd: "/Users/dev/code/mobile-ui", foreground_cwd: "/Users/dev/code/mobile-ui", title: "vite", revision: 1 },
    { pane_id: "w3:p1", terminal_id: "term_6", workspace_id: "w3", tab_id: "w3:t1", focused: false, cwd: "/Users/dev/code/infra", foreground_cwd: "/Users/dev/code/infra", title: "zsh", revision: 1 },
  ];
  const agents: WireAgent[] = [
    { terminal_id: "term_1", agent: "claude", name: "claude", agent_status: "blocked", workspace_id: "w1", tab_id: "w1:t1", pane_id: "w1:p1", focused: true, interactive_ready: true, state_change_seq: 30, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", terminal_title_stripped: "Approve this command?", revision: 3 },
    { terminal_id: "term_4", agent: "opencode", name: "opencode", agent_status: "working", workspace_id: "w2", tab_id: "w2:t1", pane_id: "w2:p1", focused: false, interactive_ready: true, state_change_seq: 20, cwd: "/Users/dev/code/mobile-ui", foreground_cwd: "/Users/dev/code/mobile-ui", terminal_title_stripped: "Refactoring mobile UI", revision: 5 },
    { terminal_id: "term_3", agent: "codex", name: "codex", agent_status: "done", workspace_id: "w1", tab_id: "w1:t2", pane_id: "w1:p3", focused: false, interactive_ready: false, state_change_seq: 10, cwd: "/Users/dev/code/space-api", foreground_cwd: "/Users/dev/code/space-api", terminal_title_stripped: "api tests", revision: 2 },
  ];
  const layouts: WireLayout[] = [
    { workspace_id: "w1", tab_id: "w1:t1", zoomed: false, area: FULL, focused_pane_id: "w1:p1", panes: [{ pane_id: "w1:p1", focused: true, rect: FULL }, { pane_id: "w1:p2", focused: false, rect: FULL }], splits: [] },
  ];
  const worktrees: WireWorktree[] = [
    { path: "/Users/dev/code/space-api", label: "auth-refactor", branch: "auth-refactor", is_bare: false, is_detached: false, is_linked_worktree: true, is_prunable: false, open_workspace_id: "w1" },
    { path: "/Users/dev/code/experiment", label: "experiment", branch: "experiment", is_bare: false, is_detached: false, is_linked_worktree: true, is_prunable: true },
  ];
  const generations: Record<string, number> = { "w1:p1": 3, "w1:p2": 1, "w1:p3": 2, "w2:p1": 5, "w2:p2": 1, "w3:p1": 1 };
  return { seq: 7, idSeq: 7, workspaces, tabs, panes, agents, layouts, worktrees, generations, focusedWorkspaceId: "w1", focusedTabId: "w1:t1", focusedPaneId: "w1:p1" };
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
    worktrees: herd.worktrees,
  };
}

function envelope() {
  const data = { seq: herd.seq, hash: `h${herd.seq}`, topology: topology(), generations: herd.generations };
  return { version: herd.seq, hash: `h${herd.seq}`, data, updated_at: new Date(clock).toISOString() };
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
    status: { version: "0.1.0", protocol: 17, mode: "quick", ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: eventClients.size },
    tunnel: { mode: "quick", public_url: "https://example.trycloudflare.com", health: { healthy: true, detail: "ready" } },
    limits: { max_body_bytes: 1048576, max_pane_read_lines: 5000, confirmation_ttl_seconds: 30 },
  };
}

// Test-only outage switch: when on, the events socket is closed + refuses
// reconnects and /snapshot returns 503, simulating a REAL disconnect (not a
// network flag that leaves the socket OPEN). Lets e2e exercise the readyState-
// based health logic deterministically across browsers.
let outage = false;

const eventClients = new Set<WebSocket>();
function broadcast() {
  herd.seq += 1;
  const msg = JSON.stringify({ type: "snapshot", snapshot: envelope() });
  for (const ws of eventClients) if (ws.readyState === ws.OPEN) ws.send(msg);
}

/* ------------------------------------------------------------- confirmations */
const nonces = new Map<string, { operation: string; resource: string; expires: number }>();
function issueNonce(operation: string, resource: string) {
  const confirmation = `cnf-${operation}-${resource}-${now()}`;
  nonces.set(confirmation, { operation, resource, expires: Date.now() + 30_000 });
  return { confirmation, expires_unix_ms: Date.now() + 30_000 };
}
function consumeNonce(confirmation: string, operation: string, resource: string): boolean {
  const n = nonces.get(confirmation);
  if (!n || n.operation !== operation || n.expires < Date.now()) return false;
  if (n.resource && resource && n.resource !== resource) return false;
  nonces.delete(confirmation);
  return true;
}

/* ---------------------------------------------------------------- mutations */
const idempotency = new Map<string, unknown>();
const CONFIRMABLE = new Set(["workspace.close", "tab.close", "pane.close", "worktree.remove", "worktree.remove_force"]);

function newPaneId(ws: string) {
  const id = `${ws}:p${herd.idSeq++}`;
  herd.generations[id] = 1;
  return id;
}

function applyMutation(body: Record<string, unknown>): { status: number; payload: unknown } {
  const requestId = String(body.request_id ?? "");
  if (requestId && idempotency.has(requestId)) return { status: 200, payload: idempotency.get(requestId) };
  const op = String(body.operation ?? "");
  const params = (body.params ?? {}) as Record<string, unknown>;
  const confirmation = body.confirmation ? String(body.confirmation) : "";

  if (CONFIRMABLE.has(op)) {
    const resource =
      op === "workspace.close" ? String(params.workspace_id ?? "")
      : op === "tab.close" ? String(params.tab_id ?? "")
      : op === "pane.close" ? String(params.pane_id ?? "")
      : String(params.worktree_id ?? params.workspace_id ?? "");
    if (!confirmation || !consumeNonce(confirmation, op, resource)) {
      return { status: 428, payload: { request_id: requestId, error: { code: "confirmation_required", message: "confirmation required", retryable: false } } };
    }
  }

  let result: Record<string, unknown> = { ok: true };
  switch (op) {
    case "workspace.create": {
      const n = herd.workspaces.length + 1;
      const id = `w${herd.idSeq++}`;
      const tabId = `${id}:t1`;
      const paneId = newPaneId(id);
      herd.workspaces.push({ workspace_id: id, number: n, label: String(params.label || `space-${n}`), focused: false, pane_count: 1, tab_count: 1, active_tab_id: tabId, agent_status: "idle" });
      herd.tabs.push({ tab_id: tabId, workspace_id: id, number: 1, label: "shell", focused: false, pane_count: 1, agent_status: "idle" });
      herd.panes.push({ pane_id: paneId, terminal_id: `term_${herd.idSeq}`, workspace_id: id, tab_id: tabId, focused: false, cwd: String(params.cwd || "/Users/dev/code"), foreground_cwd: String(params.cwd || "/Users/dev/code"), title: "zsh", revision: 0 });
      result = { workspace: { workspace_id: id }, tab: { tab_id: tabId }, root_pane: { pane_id: paneId } };
      break;
    }
    case "tab.create": {
      const wsId = String(params.workspace_id);
      const ws = herd.workspaces.find((w) => w.workspace_id === wsId);
      if (!ws) return notFound(requestId, "workspace");
      const order = herd.tabs.filter((t) => t.workspace_id === wsId).length;
      const id = `${wsId}:t${herd.idSeq++}`;
      const paneId = newPaneId(wsId);
      herd.tabs.push({ tab_id: id, workspace_id: wsId, number: order + 1, label: String(params.label || `tab-${order + 1}`), focused: false, pane_count: 1, agent_status: "idle" });
      herd.panes.push({ pane_id: paneId, terminal_id: `term_${herd.idSeq}`, workspace_id: wsId, tab_id: id, focused: false, cwd: ws.active_tab_id, foreground_cwd: "", title: "zsh", revision: 0 });
      ws.tab_count += 1;
      result = { tab: { tab_id: id }, root_pane: { pane_id: paneId } };
      break;
    }
    case "pane.split": {
      const paneId = String(params.pane_id || params.target_pane_id);
      const pane = herd.panes.find((p) => p.pane_id === paneId);
      if (!pane) return notFound(requestId, "pane");
      const id = newPaneId(pane.workspace_id);
      herd.panes.push({ pane_id: id, terminal_id: `term_${herd.idSeq}`, workspace_id: pane.workspace_id, tab_id: pane.tab_id, focused: false, cwd: pane.cwd, foreground_cwd: pane.cwd, title: "zsh", revision: 0 });
      const t = herd.tabs.find((tt) => tt.tab_id === pane.tab_id);
      if (t) t.pane_count += 1;
      result = { pane: { pane_id: id } };
      break;
    }
    case "workspace.focus":
      herd.workspaces.forEach((w) => (w.focused = w.workspace_id === params.workspace_id));
      herd.focusedWorkspaceId = String(params.workspace_id);
      break;
    case "tab.focus":
      herd.focusedTabId = String(params.tab_id);
      break;
    case "pane.focus":
      herd.focusedPaneId = String(params.pane_id);
      break;
    case "workspace.rename": {
      const w = herd.workspaces.find((x) => x.workspace_id === params.workspace_id);
      if (w) w.label = String(params.label);
      break;
    }
    case "tab.rename": {
      const t = herd.tabs.find((x) => x.tab_id === params.tab_id);
      if (t) t.label = String(params.label);
      result = { tab: t };
      break;
    }
    case "pane.rename": {
      const p = herd.panes.find((x) => x.pane_id === params.pane_id);
      if (p) p.title = String(params.label);
      break;
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
      // Write the reordered workspace tabs back into their original array slots.
      let wi = 0;
      herd.tabs = herd.tabs.map((x) => (x.workspace_id === t.workspace_id ? without[wi++] : x));
      result = { tabs: without };
      break;
    }
    case "pane.zoom": {
      const p = herd.panes.find((x) => x.pane_id === params.pane_id);
      const layout = herd.layouts.find((l) => l.tab_id === p?.tab_id);
      if (layout) layout.zoomed = !layout.zoomed;
      break;
    }
    case "pane.resize":
    case "pane.swap":
    case "agent.focus":
    case "agent.send_keys":
    case "worktree.open":
      break;
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
      // new_tab / new_workspace are acknowledged; the mock leaves layout otherwise unchanged.
      break;
    }
    case "workspace.close":
      herd.workspaces = herd.workspaces.filter((w) => w.workspace_id !== params.workspace_id);
      herd.tabs = herd.tabs.filter((t) => t.workspace_id !== params.workspace_id);
      herd.panes = herd.panes.filter((p) => p.workspace_id !== params.workspace_id);
      herd.agents = herd.agents.filter((a) => a.workspace_id !== params.workspace_id);
      break;
    case "tab.close":
      herd.tabs = herd.tabs.filter((t) => t.tab_id !== params.tab_id);
      herd.panes = herd.panes.filter((p) => p.tab_id !== params.tab_id);
      herd.agents = herd.agents.filter((a) => a.tab_id !== params.tab_id);
      break;
    case "pane.close":
      herd.panes = herd.panes.filter((p) => p.pane_id !== params.pane_id);
      herd.agents = herd.agents.filter((a) => a.pane_id !== params.pane_id);
      delete herd.generations[String(params.pane_id)];
      break;
    case "agent.prompt": {
      const a = herd.agents.find((x) => x.pane_id === params.pane_id || x.name === params.target);
      if (a) {
        a.agent_status = "working";
        a.state_change_seq = now();
        const p = herd.panes.find((x) => x.pane_id === a.pane_id);
        if (p) p.agent_status = "working";
      }
      break;
    }
    case "agent.rename": {
      const a = herd.agents.find((x) => x.pane_id === params.pane_id || x.name === params.target);
      if (a) a.name = String(params.name);
      break;
    }
    case "agent.start": {
      const paneId = String(params.pane_id);
      const kind = String(params.kind);
      const name = String(params.name ?? "");
      // Mirror internal/herdr/agents.go ValidAgentName + uniqueness.
      if (!/^[a-z][a-z0-9_-]{0,31}$/.test(name)) {
        return { status: 400, payload: { request_id: requestId, error: { code: "invalid_params", message: "invalid agent name", retryable: false } } };
      }
      if (herd.agents.some((a) => a.name === name)) {
        return { status: 409, payload: { request_id: requestId, error: { code: "conflict", message: "agent name in use", retryable: false } } };
      }
      const p = herd.panes.find((x) => x.pane_id === paneId);
      if (!p) return notFound(requestId, "pane");
      p.agent = kind;
      p.display_agent = name;
      p.agent_status = "working";
      herd.agents.push({ terminal_id: p.terminal_id, agent: kind, name, agent_status: "working", workspace_id: p.workspace_id, tab_id: p.tab_id, pane_id: paneId, focused: false, interactive_ready: true, state_change_seq: now(), cwd: p.cwd, foreground_cwd: p.cwd, terminal_title_stripped: `${name} starting`, revision: 0 });
      result = { agent: { name, agent: kind } };
      break;
    }
    case "worktree.create":
      herd.worktrees.push({ path: String(params.path || `${params.cwd}/wt`), label: String(params.label || "feature"), branch: String(params.branch || "feature"), is_bare: false, is_detached: false, is_linked_worktree: true, is_prunable: false });
      break;
    case "worktree.remove":
    case "worktree.remove_force":
      herd.worktrees = herd.worktrees.filter((w) => w.open_workspace_id !== params.worktree_id && w.open_workspace_id !== params.workspace_id);
      break;
    default:
      return { status: 400, payload: { request_id: requestId, error: { code: "bad_request", message: `unknown operation ${op}`, retryable: false } } };
  }

  const payload = { request_id: requestId, accepted: true, result };
  if (requestId) idempotency.set(requestId, payload);
  broadcast();
  return { status: 200, payload };
}

function notFound(requestId: string, kind: string) {
  return { status: 404, payload: { request_id: requestId, error: { code: "not_found", message: `${kind} not found`, retryable: false } } };
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
function isPaired(req: IncomingMessage): boolean {
  return (req.headers.cookie ?? "").includes(`${COOKIE}=`);
}
function unauthorized(res: ServerResponse): boolean {
  send(res, 401, { error: { code: "unauthorized", message: "pairing required", retryable: false } });
  return true;
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
    outage = false;
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
  if (path === "/pair" && method === "POST") {
    const body = await readBody(req);
    if (String(body.secret) !== PAIR_SECRET) {
      send(res, 401, { error: { code: "unauthorized", message: "pairing rejected", retryable: false } });
      return true;
    }
    send(res, 200, {
      csrf_token: "mock-csrf-token",
      expires_unix_ms: Date.now() + 12 * 3600 * 1000,
      identity: { subject: "", display: "Quick Tunnel operator", quick: true, mode: "quick" },
    }, { "Set-Cookie": `${COOKIE}=1; Path=/; HttpOnly; SameSite=Strict` });
    return true;
  }
  if (path === "/session" && method === "GET") {
    if (!isPaired(req)) return unauthorized(res);
    // Same shape as /pair: carries csrf_token + expiry so a cold reload recovers
    // a mutable session. The bearer cookie is never echoed in the body.
    send(res, 200, {
      csrf_token: "mock-csrf-token",
      expires_unix_ms: Date.now() + 12 * 3600 * 1000,
      identity: { subject: "", display: "Quick Tunnel operator", quick: true, mode: "quick" },
    });
    return true;
  }
  if (path === "/session" && method === "DELETE") {
    res.writeHead(204, { "Set-Cookie": `${COOKIE}=; Path=/; Max-Age=0` });
    res.end();
    return true;
  }
  if (path === "/capabilities" && method === "GET") {
    if (!isPaired(req)) return unauthorized(res);
    send(res, 200, capabilities());
    return true;
  }
  if (path === "/snapshot" && method === "GET") {
    if (!isPaired(req)) return unauthorized(res);
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
    if (!isPaired(req)) return unauthorized(res);
    const paneId = decodeURIComponent(path.slice("/panes/".length, -"/read".length));
    send(res, 200, { pane_id: paneId, source: url.searchParams.get("source") ?? "visible", lines: Number(url.searchParams.get("lines") ?? 100), content: `Last output for ${paneId}\n$ ` });
    return true;
  }
  if (path === "/directories" && method === "GET") {
    if (!isPaired(req)) return unauthorized(res);
    const p = url.searchParams.get("path") ?? "/Users/dev/code";
    send(res, 200, { path: p, entries: [{ name: "space-api", path: `${p}/space-api` }, { name: "mobile-ui", path: `${p}/mobile-ui` }, { name: "infra", path: `${p}/infra` }] });
    return true;
  }
  if (path === "/confirmations" && method === "POST") {
    if (!isPaired(req)) return unauthorized(res);
    const body = await readBody(req);
    send(res, 200, issueNonce(String(body.operation), String(body.resource_id ?? "")));
    return true;
  }
  if (path === "/mutations" && method === "POST") {
    if (!isPaired(req)) return unauthorized(res);
    const body = await readBody(req);
    const { status, payload } = applyMutation(body);
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
    /* browser only sends control frames; nothing to act on */
  });
});

terminalWss.on("connection", (ws, req) => {
  const url = new URL(req.url ?? "", "http://localhost");
  const paneId = decodeURIComponent(url.pathname.split("/terminals/")[1] ?? "");
  const confirmation = url.searchParams.get("confirmation") ?? "";
  // Takeover is honored only with a valid terminal.takeover confirmation nonce
  // (mirrors internal/server/terminalroute.go).
  const takeover = url.searchParams.get("takeover") === "1" && consumeNonce(confirmation, "terminal.takeover", paneId);

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
      // separate rows in the mirror (a fake-shell convenience for tests).
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
  if (!isPaired(req)) {
    socket.destroy();
    return;
  }
  if (url.pathname === "/api/v1/events") {
    if (outage) {
      socket.destroy();
      return;
    }
    eventsWss.handleUpgrade(req, socket, head, (ws) => eventsWss.emit("connection", ws, req));
  } else if (url.pathname.startsWith("/api/v1/terminals/")) {
    terminalWss.handleUpgrade(req, socket, head, (ws) => terminalWss.emit("connection", ws, req));
  } else {
    socket.destroy();
  }
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
