/**
 * Normalizing the experimental interpreted parts (SPEC §12.2).
 *
 * This is the client half of a deliberately narrow contract. The relay does the
 * pattern matching; this module's job is to refuse anything it does not
 * recognize, so that a future relay adding a turn kind, an interaction type, or a
 * diff op cannot make this build render something it does not understand.
 *
 * Three rules apply throughout, and every one of them exists because this data is
 * a *guess* about a third-party TUI rather than something an agent published:
 *
 *  1. **Unknown values are dropped, never coerced.** An unrecognized turn kind is
 *     not rendered as prose; an unrecognized interaction type is not rendered as
 *     an approval. Guessing here would put words in the agent's mouth.
 *  2. **`answerable` is the only gate on offering an action.** Never
 *     `options.length`. A prompt can be fully described and still be impossible
 *     to answer remotely — that is exactly the OpenCode case, where which button
 *     is highlighted is carried by ANSI styling the relay's text read discards.
 *  3. **A send key is used verbatim or not at all.** It is validated against the
 *     same single-digit allowlist the relay synthesizes it from. The client never
 *     derives a key from a label, an index, or a position.
 */
import type {
  WireInterpretedInteractionPart,
  WireInterpretedTranscriptPart,
  WireRunPart,
} from "./types";

/** Part type names, matching internal/server/runs.go. */
export const PART_INTERPRETED_TRANSCRIPT = "interpreted_transcript";
export const PART_INTERPRETED_INTERACTION = "interpreted_interaction";

/** Turn kinds this build renders. Anything else is dropped. */
export const TURN_KINDS = ["agent_text", "tool_call", "tool_result", "status"] as const;
export type TurnKind = (typeof TURN_KINDS)[number];

export const INTERACTION_KINDS = ["approval", "question"] as const;
export type InteractionKind = (typeof INTERACTION_KINDS)[number];

export const DIFF_OPS = ["context", "add", "remove"] as const;
export type DiffOp = (typeof DIFF_OPS)[number];

export interface InterpretedTurn {
  /** Stable within one read, for React keys. The relay publishes no turn ids. */
  id: string;
  kind: TurnKind;
  /** Apparent tool name; only ever set on a `tool_call`. */
  tool: string;
  text: string;
}

export interface InterpretedOption {
  label: string;
  /** A single digit 1-9, or null when this option cannot be answered remotely. */
  sendKey: string | null;
}

export interface InterpretedDiffLine {
  line: number | null;
  op: DiffOp;
  text: string;
}

export interface InterpretedInteraction {
  parser: string;
  kind: InteractionKind;
  title: string;
  detail: string[];
  question: string;
  /**
   * True only when the relay said so *and* every surviving option kept a valid
   * key. Both conditions are required: dropping an option during normalization
   * must not leave a partially-answerable prompt presented as answerable.
   */
  answerable: boolean;
  options: InterpretedOption[];
  diff: InterpretedDiffLine[];
}

export interface InterpretedTranscript {
  parser: string;
  turns: InterpretedTurn[];
  droppedTurns: number;
  droppedLines: number;
  /**
   * True when the first turn is the tail of something that began before the read
   * window. A bounded read of a busy pane hits this often, so the UI says so rather
   * than letting a fragment read as a whole answer.
   */
  startsMidTurn: boolean;
}

export interface Interpretation {
  transcript: InterpretedTranscript | null;
  interaction: InterpretedInteraction | null;
  /** Turns dropped here because this build does not know their kind. */
  unknownTurnKinds: string[];
}

export const EMPTY_INTERPRETATION: Interpretation = {
  transcript: null,
  interaction: null,
  unknownTurnKinds: [],
};

/**
 * A send key is accepted only if it is exactly one digit 1-9 — the same allowlist
 * the relay validates against before publishing it. Anything else becomes null,
 * which removes the action rather than sending a guess.
 */
export function validSendKey(value: unknown): value is string {
  return typeof value === "string" && /^[1-9]$/.test(value);
}

function isTurnKind(value: unknown): value is TurnKind {
  return typeof value === "string" && (TURN_KINDS as readonly string[]).includes(value);
}

function isInteractionKind(value: unknown): value is InteractionKind {
  return typeof value === "string" && (INTERACTION_KINDS as readonly string[]).includes(value);
}

function isDiffOp(value: unknown): value is DiffOp {
  return typeof value === "string" && (DIFF_OPS as readonly string[]).includes(value);
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

/**
 * Extract the interpreted parts from a run response.
 *
 * `enabled` is the capability gate. When the relay has not advertised
 * `heuristic_interpretation`, the parts are ignored even if they are present —
 * the UI renders what was advertised, not what happened to arrive.
 */
export function readInterpretation(parts: WireRunPart[] | undefined, enabled: boolean): Interpretation {
  if (!enabled || !Array.isArray(parts)) return EMPTY_INTERPRETATION;

  let transcript: InterpretedTranscript | null = null;
  let interaction: InterpretedInteraction | null = null;
  const unknownTurnKinds = new Set<string>();

  for (const part of parts) {
    if (!part || typeof part !== "object") continue;
    const type = (part as { type?: unknown }).type;

    if (type === PART_INTERPRETED_TRANSCRIPT && !transcript) {
      transcript = normalizeTranscript(part as WireInterpretedTranscriptPart, unknownTurnKinds);
      continue;
    }
    if (type === PART_INTERPRETED_INTERACTION && !interaction) {
      interaction = normalizeInteraction(part as WireInterpretedInteractionPart);
    }
  }

  return { transcript, interaction, unknownTurnKinds: [...unknownTurnKinds] };
}

function normalizeTranscript(
  part: WireInterpretedTranscriptPart,
  unknownTurnKinds: Set<string>,
): InterpretedTranscript | null {
  const raw = Array.isArray(part.turns) ? part.turns : [];
  const turns: InterpretedTurn[] = [];

  raw.forEach((turn, index) => {
    if (!turn || typeof turn !== "object") return;
    if (!isTurnKind(turn.kind)) {
      // Counted so the UI can say something was withheld, rather than silently
      // presenting an incomplete transcript as complete.
      if (typeof turn.kind === "string" && turn.kind) unknownTurnKinds.add(turn.kind);
      return;
    }
    const body = text(turn.text);
    const tool = turn.kind === "tool_call" ? text(turn.tool) : "";
    if (!body && !tool) return;
    turns.push({ id: `${index}-${turn.kind}`, kind: turn.kind, tool, text: body });
  });

  if (turns.length === 0) return null;
  return {
    parser: text(part.parser),
    turns,
    droppedTurns: Number.isFinite(part.dropped_turns) ? Math.max(0, part.dropped_turns) : 0,
    droppedLines: Number.isFinite(part.dropped_lines) ? Math.max(0, part.dropped_lines) : 0,
    startsMidTurn: part.starts_mid_turn === true,
  };
}

function normalizeInteraction(part: WireInterpretedInteractionPart): InterpretedInteraction | null {
  if (!isInteractionKind(part.interaction)) return null;

  const rawOptions = Array.isArray(part.options) ? part.options : [];
  const options: InterpretedOption[] = [];
  for (const option of rawOptions) {
    if (!option || typeof option !== "object") continue;
    const label = text(option.label);
    if (!label) continue;
    options.push({ label, sendKey: validSendKey(option.send_key) ? option.send_key : null });
  }

  const diff: InterpretedDiffLine[] = [];
  for (const line of Array.isArray(part.diff) ? part.diff : []) {
    if (!line || typeof line !== "object" || !isDiffOp(line.op)) continue;
    diff.push({
      line: typeof line.line === "number" && Number.isFinite(line.line) ? line.line : null,
      op: line.op,
      text: text(line.text),
    });
  }

  const title = text(part.title);
  const question = text(part.question);
  if (!title && !question && options.length === 0) return null;

  // Both conditions, deliberately. The relay's flag alone is not enough, because
  // an option may have been dropped above for carrying an invalid key.
  const answerable = part.answerable === true && options.length > 0 && options.every((o) => o.sendKey !== null);

  return {
    parser: text(part.parser),
    kind: part.interaction,
    title,
    detail: stringList(part.detail),
    question,
    answerable,
    options,
    diff,
  };
}
