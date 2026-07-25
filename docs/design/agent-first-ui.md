# Agent-first UI rewrite

Status: approved implementation direction

This document is the product, UX, architecture, security, and delivery contract for
the Herdr Phone agent-first rewrite. It replaces the terminal-first presentation,
not the relay's security model or Herdr's workspace, tab, pane, and agent model.

## Decision

Herdr Phone will become a mobile control plane for supervising local coding agents.
The default experience will be organized around agent runs presented as
conversations. The terminal remains available as an expert recovery and takeover
surface, but it is no longer the home screen or a primary navigation destination.

The rewrite must not fake a conversation by styling terminal bytes as assistant
messages. A message, activity, approval, diff, or test result may only be rendered
semantically when the relay receives authoritative structured data for it. When
structured data is unavailable, the UI must identify the content as recent terminal
output and keep the console easy to reach.

The implementation is a UI and product rewrite, not a rewrite of the proven pairing,
Access JWT, session, CSRF, Origin, confirmation, audit, topology, or terminal-filtering
foundations.

## Product job

The audience is a developer away from their workstation who needs to supervise one
local Herdr session safely from a phone. The product's single job is to make the
decisions that keep agent work moving easy to understand and safe to perform:

- See which agents need attention.
- Start correctly scoped work.
- Give an agent an instruction.
- Understand observed progress without reading a terminal repaint.
- Review an authoritative question, command, result, or failure.
- Recover through the full console when structured controls are insufficient.

The phone is a control plane. It is not a miniature terminal or IDE.

## Reference research

The following current products converge on the control-plane model:

- OpenAI Codex Remote explicitly describes the phone as an engineering control
  plane. Its useful patterns include host and workspace selection before prompting,
  separate queue and steer semantics, side chats, changed-file review, narrow
  permissions, notifications, and archived chats.
  <https://developers.openai.com/blog/mastering-codex-remote-for-engineering>
- Claude Code Remote Control keeps execution and tools local while synchronizing a
  conversation, subagent progress, attachments, reconnect state, and decisions across
  terminal, browser, and phone.
  <https://code.claude.com/docs/en/remote-control>
- Cursor Mobile centers an agent inbox, follow-up instructions, needs-input and
  ready-for-review transitions, evidence artifacts, diffs, and handoff. It
  intentionally does not reproduce the complete editor or terminal.
  <https://cursor.com/blog/ios-mobile-app>
- GitHub Copilot agent sessions expose progress, tool calls, tests, review, and PR
  state while keeping execution context explicit.
  <https://docs.github.com/en/copilot/how-tos/copilot-on-github/use-copilot-agents/manage-and-track-agents>
- AI Elements is a useful source-owned shadcn reference for conversation, message,
  composer, task, plan, tool, confirmation, code, and test-result presentation.
  Herdr Phone will borrow presentation patterns selectively, not its AI SDK transport
  or generic approval model.
  <https://ai-sdk.dev/elements/overview>

## Current assessment

### Useful foundations to retain

- Pairing, Access JWT validation, in-memory app sessions, CSRF, and exact Origin
  handling.
- The single server route table and typed mutation allowlist.
- Snapshot-as-truth and events-as-wakeups state reconciliation.
- Pane lifecycle generations and confirmation nonces.
- `useSyncExternalStore` and the existing REST/WebSocket client boundary.
- Workspace, tab, pane, worktree, and agent topology normalization.
- Workspace/worktree creation and the agent kind discovery APIs.
- The ANSI-filtered xterm.js console and terminal takeover flow.
- Source-owned Radix/shadcn-style primitives.
- Safe-area, dynamic viewport, keyboard, focus, and reduced-motion foundations.

### Problems the rewrite must solve

- `/` and `/terminal` currently make a selected xterm pane the primary object.
- The workspace, tab, and pane ribbon consumes three rows on every screen.
- Herd is a status-card screen without an agent detail or conversation route.
- Prompting is a modal, one-shot action; replies are visible only in the terminal.
- Starting useful work is an implementation-shaped sequence: create workspace, locate
  pane actions, start agent, then prompt.
- Workspace, tab, pane, and agent controls dominate the interface instead of being
  execution context.
- The current visual language uses too many outlined boxes, tiny uppercase labels,
  and utility typography for a conversational product.
- Terminal input may be cleared while disconnected, without a delivery state.
- Offline, unsupported-capability, partial-creation, and recovery paths are weak.
- Opening or focusing an item can conflate reading remote state with changing local
  Herdr focus.

### Contract defects to fix before relying on existing mutations

The frontend currently omits the mandatory pane generation from several pane and
agent operations, including prompt, logical keys, focus, and rename. Terminal
reconnect also drops its generation. The server rejects these requests, while the
mock relay does not enforce the production contract. The rewrite must:

- Pass canonical `pane_id` and nonzero `expected_generation` for every pane-scoped
  mutation.
- Preserve generation across terminal reconnects.
- Stop using mutable agent names or the alternate `target` field as dispatch
  identity.
- Make the mock reject the same malformed requests as production.
- Add regression tests proving stale and absent generations fail.

## Product model

The user-facing objects are intentionally simpler than the execution topology:

| User-facing object | Meaning | Execution identity |
| --- | --- | --- |
| Run | One live agent objective and its control history | Conversation incarnation attached to a pane generation and agent session |
| Workspace | The project or worktree in which work runs | Herdr workspace, tabs, panes, and worktree metadata |
| Agent | The active coding agent | Agent kind and session occupying a pane |
| Console | Full-fidelity recovery and direct control | Existing terminal WebSocket and takeover flow |

Workspace, tab, pane, generation, agent session, current directory, and worktree
remain inspectable. They are not flattened into one ambiguous chat identifier.

## Information architecture

### Agents

The default route is an attention-first run inbox. Sections are ordered by urgency:

1. Needs you
2. Working
3. Updated
4. Idle
5. Status unknown

`done` means unseen background work settled; it must be presented as Updated, not
Ready or Successful. `unknown` must never be grouped with Idle or complete.

Rows show the objective or best authoritative title, agent identity, workspace and
worktree context, current status, and last authoritative activity time when one
exists. Live updates must not move a row while it has pointer or keyboard interaction.

Opening a run is read-only with respect to local Herdr focus. A separate explicit
control may focus the underlying agent when needed.

### Run

A run is a conversation-like document with:

- Sticky back navigation, title, textual status, and overflow menu.
- An expandable context line near both the header and composer:
  `workspace / worktree / tab / agent`.
- User instructions.
- Agent responses when structured messages are supported.
- A vertical runline of authoritative, collapsible activity parts.
- Typed attention, approval, command, plan, diff, test, and failure parts only when
  supplied by an authoritative contract.
- Clear pending, accepted, streaming, complete, interrupted, failed, and
  delivery-unknown states.
- A sticky composer with the exact target visible before sending.
- A one-tap Open console action for blocked, unknown, unsupported, or recovery states.

Agent prose should read like a document, not a stack of symmetric chat bubbles. User
instructions may use compact tinted blocks. Activity belongs in the runline rather
than as assistant prose.

### Start run

The primary creation journey begins with intent, not topology:

1. Describe the objective.
2. Choose an existing workspace, a new workspace, or a new worktree.
3. Choose the agent kind and optional name.
4. Review execution context and start.

Workspace, pane, agent, and first-instruction creation are separate server operations.
The UI must show a launch receipt with recoverable steps and preserve partial success.
It must not claim the sequence is transactional or silently delete a successfully
created resource after a later step fails.

The launch route must be resumable. If it is presented as the center navigation item,
it is a real route, not a transient button whose state disappears on accidental
dismissal.

### Workspaces

Workspaces become the secondary inspect-and-manage surface. The first level emphasizes
project/worktree identity and active runs, not raw tab and pane counts. A workspace
detail surface exposes tabs, panes, empty shells, generations, layout actions, rename,
move, split, close, and worktree removal under explicit advanced controls.

### Console

The console is lazy-loaded from a run or pane menu. The existing filtered xterm.js,
logical key dock, resize, conflict, and takeover behavior remains. It is never replaced
with a `<pre>`-based pseudo-terminal. Terminal ownership and takeover remain explicit.

## Mobile layout

```text
AGENTS                              + START

NEEDS YOU · 2
+----------------------------------------+
| Claude · Fix auth refresh              |
| A decision is required                 |
| space-api / auth-refactor · 3m          |
|                                  Open  |
+----------------------------------------+

WORKING · 3
+----------------------------------------+
| OpenCode · Mobile navigation           |
| Editing 3 files · tests running        |
| mobile-ui / feature/chat               |
+----------------------------------------+

Agents              Start          Workspaces
```

```text
< Fix auth refresh          NEEDS YOU   ...
space-api / auth-refactor / Claude
------------------------------------------

You
Make reconnect preserve the active session.

| Searched 12 files
| Edited 3 files
| Running focused tests             runline

Claude
The reconnect path was dropping the pane generation...

+----------------------------------------+
| Command needs review                   |
| go test ./internal/server/...          |
| /code/space-api                        |
|                           Deny  Review |
+----------------------------------------+

[ Add an instruction...                ] ^
```

The screen has one scroll owner above the software keyboard. Header, composer, safe
areas, and dynamic viewport behavior must work at 320 px width, text zoom, short
landscape heights, and installed-PWA display modes.

## Wide layout

Desktop and tablet use one responsive component tree. At sufficient width the Agents
inbox becomes a stable left column and the selected run occupies a bounded reading
column. Workspaces or run context may open as a third inspector column. Crossing a
breakpoint must not unmount a live run or console unnecessarily.

Container queries should be preferred for cards and inspectors whose useful layout
depends on their allocated column rather than the entire viewport.

## Visual direction: Dispatch Log

The visual identity evolves the existing field instrument into a quieter dispatch
log. It must not become a stock shadcn dashboard or a visual clone of ChatGPT.

Primary dark tokens:

- Deck `#101820`: application background.
- Bulkhead `#192732`: raised navigation and control surfaces.
- Mist `#dce7e4`: primary text.
- Brass `#e3b341`: selected context and deliberate primary actions.
- Tide `#50a8a3`: connected and active state.
- Flare `#f1745e`: attention and destructive state.

Light mode must retain the same semantic relationships with independently
contrast-checked values.

Typography starts from a shadcn-compatible token system rather than the current
field-console typography:

- A new, highly legible sans family is used for navigation, conversation, and body
  copy. It must be self-hosted and selected during implementation after visual
  comparison at phone sizes.
- Commit Mono, or a replacement self-hosted mono, is restricted to commands, paths,
  IDs, timestamps, and tabular data.
- Utility labels use sentence case. Tiny uppercase mono labels are removed.
- Body copy targets comfortable 15-17 px phone reading sizes and line lengths.

The signature element is the runline: a restrained vertical flight recorder that
encodes chronological structured activity. It is the single distinctive visual risk.
Everything around it remains quiet and precise.

Motion is limited to one orchestrated behavior: new runline activity settling into
place. Status must never rely on animation, and reduced-motion preferences disable the
effect.

## Component strategy

The rewrite begins from source-owned shadcn component conventions and semantic CSS
variables. Existing primitives may be replaced where their API or styling no longer
fits. The repository must continue to own the resulting source.

Expected primitives include Button, Badge, Textarea, Input, Label, Separator,
DropdownMenu, Sheet, Dialog, AlertDialog, Collapsible, Tabs, Command, ScrollArea or
native overflow, Skeleton, Tooltip, and accessible form patterns.

AI Elements may be used as source reference as follows:

- Adapt Conversation scrolling, `role="log"`, jump-to-latest, and empty-state
  behavior.
- Adapt the basic Message and MessageContent layout without adopting generic markdown
  or transport assumptions.
- Adapt Prompt Input auto-sizing and IME-safe keyboard handling to the Herdr mutation
  API.
- Add Plan, Task, Tool, Code Block, and Test Results only after the backend supplies
  typed authoritative parts.
- Do not add `useChat`, an AI SDK transport, generic `/api/chat`, transcript export,
  generic tool dispatch, permissive raw HTML, or AI Elements' approval mechanics.
- Keep the existing nonce-backed confirmation flow.
- Keep xterm.js for the console.

## Structured run contract

The current browser API has no message or conversation route. A real agent-first
experience requires a versioned structured source from Herdr or trusted agent
adapters. Terminal parsing is not an acceptable source of message boundaries,
assistant roles, tool calls, approvals, diffs, or test results.

Minimum conceptual model:

```text
Run
  id
  pane_id
  pane_generation
  agent_session/incarnation
  workspace_id
  tab_id
  revision
  status
  objective/title

Message
  id
  run_id
  sequence
  role: user | assistant | system | tool
  parts[]
  state: pending | accepted | streaming | complete | interrupted | failed |
         delivery_unknown
  created_at
  completed_at
  client_message_id

Interaction
  id
  run_id
  message_id
  kind
  prompt/context
  choices with opaque IDs
  state
  expiry
```

Recommended browser contract:

- A bounded, paginated run/messages read endpoint guarded by the current pane
  generation and run incarnation.
- A separate current-run summary endpoint or snapshot projection that contains no
  full transcript bodies.
- Events that announce a run revision change and cause a truth refetch. Events do not
  mutate message state directly.
- A typed send operation with canonical pane ID, generation, run incarnation,
  client-message ID, byte limits, and end-to-end idempotency.
- A typed interaction-response operation bound to interaction, run, pane generation,
  and choice ID.
- Explicit capability advertisement and protocol versioning. The UI fails closed to
  terminal-output mode when structured runs are unsupported.

Full transcript content must not be added to the topology snapshot. High-frequency
run state belongs in a separate bounded store so it does not rerender or broadcast the
entire application state.

## Delivery and idempotency

The current relay can lose certainty after Herdr accepts a prompt but before the HTTP
response is returned. Automatic retry can therefore duplicate an instruction. The new
send contract must either provide upstream idempotency keyed by a client message ID or
return `delivery_unknown` and require an explicit user decision. A timed-out message
must never be silently retried.

Request-ID replay entries must be bound to a hash of operation and parameters so one
ID cannot retrieve a response for a different payload.

## Security requirements

The rewrite must preserve or strengthen all existing security invariants:

- Every new route uses the central route table and existing middleware order.
- Mutations remain a typed allowlist. There is no browser-provided method name or raw
  Herdr RPC.
- Every run and interaction is bound to a pane generation and agent incarnation.
- Structured interaction responses never infer a choice from terminal text, screen
  position, or a raw `y` key.
- Destructive interactions use the existing single-use, parameter-bound confirmation
  mechanism.
- Message bodies, terminal content, commands, and tool output are never logged or
  placed in audit records. Audit uses IDs, byte counts, categories, and outcomes.
- Run content uses `Cache-Control: no-store` and is not cached by the service worker.
- No transcript is written to disk unless `SECURITY.md` and the threat model are
  explicitly revised and reviewed.
- Plain text is the safe baseline. Any markdown implementation disables raw HTML,
  unsafe protocols, remote images, Mermaid, and untrusted embedded content.
- Code, diff, test, and terminal content is bounded and rendered as text.
- Existing ANSI filtering remains mandatory for console and terminal-derived output.
- The listener remains loopback-only and no shell invocation is introduced.
- Pairing continues to warn that this grants shell-equivalent access.

## State and frontend architecture

- Keep topology in the existing external AppStore.
- Introduce partitioned run stores keyed by run incarnation so high-frequency message
  revisions do not rerender topology consumers.
- Continue using `useSyncExternalStore`; do not add a client data framework.
- Route selection is run-centric and must not mutate local Herdr focus.
- A run becomes invalid when its pane generation or agent incarnation changes. The UI
  freezes the old run and asks the user to reopen the new occupant instead of silently
  rebinding.
- Console modules and xterm dependencies are lazy-loaded.
- Creation is a resumable orchestration state machine with explicit partial success.
- Capability gates determine whether the run renders structured chat, an observed
  activity log, or recent terminal output.

Proposed route model:

```text
/                         Agents inbox
/runs/new                 Start run
/runs/:runId              Run detail
/workspaces               Workspace list
/workspaces/:workspaceId  Workspace detail
/console/:paneId          Lazy console
/settings                 Settings
```

## Accessibility and mobile requirements

- Text and icons accompany every color status.
- Interactive targets are at least 44 by 44 CSS pixels.
- The runline is a semantic ordered list with accessible disclosure controls.
- New attention is announced once; ordinary streaming updates do not flood live
  regions.
- Route titles, headings, focus restoration, back navigation, and skip navigation are
  implemented.
- Keyboard, screen-reader, IME composition, text zoom, and reduced motion are tested.
- The composer does not clear until the instruction is accepted or explicitly enters
  a delivery-unknown state.
- Reconnect distinguishes phone offline, relay reconnecting, host unavailable, pane
  replaced, agent ended, and console ownership conflict.
- The interface supports 320, 390, 430, 768, and wide desktop layouts plus short
  landscape viewports.

## Test contract

The mock relay must model the production wire contract, including:

- Mandatory pane generations and stale-generation failures.
- Structured run and message history with revisions and pagination.
- Pending, streaming, completed, failed, interrupted, and delivery-unknown messages.
- Blocked, working, done/Updated, idle, and unknown agents.
- Run invalidation after an occupant generation change.
- Partial Start run failures at every orchestration step.
- Reconnect and missed-wakeup reconciliation.
- Confirmation expiry, mismatch, and single use.
- Console ownership conflict and takeover.

Required browser journeys include:

- Pair, land in Agents, and open an existing run.
- Start a run in an existing workspace.
- Start a run in a new workspace or worktree.
- Recover from a successful workspace creation followed by failed agent start.
- Send an instruction and observe accepted through complete states.
- Preserve draft input across connection loss.
- Handle delivery unknown without duplicate retry.
- Review attention and open the console.
- Invalidate a run after pane generation replacement.
- Manage a workspace through advanced controls.
- Exercise phone keyboard, safe areas, text zoom, reduced motion, and screen-reader
  semantics.
- Verify production dark and light screenshots at phone, tablet, and desktop widths.

The full `make check` gate and the Playwright mobile suite must pass without retries.
Release-shaped changes also run `govulncheck ./cmd/... ./internal/...` and `npm audit`
for an explicit report.

## Delivery phases

1. Repair generation propagation, production/mock parity, and mutation error fidelity.
2. Add the bounded structured run contract, capability gates, state provider, and
   deterministic mock fixtures.
3. Replace the visual tokens, typography, root shell, navigation, and responsive
   layout with the shadcn-based Dispatch Log system.
4. Build Agents, Start run, run detail, composer, observed activity, and explicit
   recovery behavior.
5. Rebuild Workspaces around active runs while retaining complete advanced topology
   controls.
6. Move the existing terminal behind a lazy console route and verify takeover.
7. Add typed interaction, review, diff, test, and plan parts only where authoritative
   data exists.
8. Complete accessibility, security, browser, race, coverage, vulnerability, plugin,
   and release verification.

## Completion criteria

The rewrite is complete when:

- Agents, not Terminal, is the default product surface.
- A developer can start, supervise, instruct, and recover an agent run without
  understanding panes during the normal journey.
- Exact execution context remains inspectable before input and destructive actions.
- No terminal bytes are misrepresented as structured messages or approvals.
- Existing security invariants and terminal filtering remain intact.
- Production and mock generation behavior agree.
- The new UI is visually distinct, accessible, responsive, and no longer resembles
  the current field-console card layout.
- All local gates, end-to-end journeys, release consistency checks, and independent
  review pass.
