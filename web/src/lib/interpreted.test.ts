import { describe, it, expect } from "vitest";
import {
  EMPTY_INTERPRETATION,
  PART_INTERPRETED_INTERACTION,
  PART_INTERPRETED_TRANSCRIPT,
  readInterpretation,
  validSendKey,
} from "./interpreted";
import type { WireRunPart } from "./types";

/**
 * These tests pin the three rules that keep the experimental chat honest
 * (SPEC §12.2): unknown values are dropped rather than coerced, `answerable` is
 * the only gate on offering an action, and a send key is used verbatim or not at
 * all.
 */

function transcriptPart(turns: Array<Record<string, unknown>>, extra: Record<string, unknown> = {}): WireRunPart {
  return {
    type: PART_INTERPRETED_TRANSCRIPT,
    parser: "claude",
    experimental: true,
    turns,
    dropped_turns: 0,
    dropped_lines: 0,
    ...extra,
  } as unknown as WireRunPart;
}

function interactionPart(extra: Record<string, unknown> = {}): WireRunPart {
  return {
    type: PART_INTERPRETED_INTERACTION,
    parser: "claude",
    experimental: true,
    interaction: "approval",
    title: "Bash command",
    detail: ['echo "hello" >> notes.txt'],
    question: "Do you want to proceed?",
    answerable: true,
    options: [
      { label: "Yes", send_key: "1" },
      { label: "No", send_key: "2" },
    ],
    ...extra,
  } as unknown as WireRunPart;
}

const observed: WireRunPart = {
  type: "observed_terminal_output",
  source: "recent-unwrapped",
  format: "text",
  lines: 2,
  bytes: 10,
  truncated: false,
  text: "hello\n",
};

describe("readInterpretation — capability gating", () => {
  it("ignores interpreted parts when the relay has not advertised the feature", () => {
    // The parts are present, but unadvertised. The UI renders what was advertised.
    const got = readInterpretation([observed, transcriptPart([{ kind: "agent_text", text: "hi" }])], false);
    expect(got).toEqual(EMPTY_INTERPRETATION);
  });

  it("returns the empty interpretation for a missing part list", () => {
    expect(readInterpretation(undefined, true)).toEqual(EMPTY_INTERPRETATION);
  });

  it("reads the transcript when advertised", () => {
    const got = readInterpretation([observed, transcriptPart([{ kind: "agent_text", text: "hi" }])], true);
    expect(got.transcript?.parser).toBe("claude");
    expect(got.transcript?.turns).toHaveLength(1);
    expect(got.transcript?.turns[0]).toMatchObject({ kind: "agent_text", text: "hi", tool: "" });
  });
});

describe("readInterpretation — unknown values are dropped, never coerced", () => {
  it("drops a turn whose kind this build does not know and reports it", () => {
    const got = readInterpretation(
      [
        transcriptPart([
          { kind: "agent_text", text: "kept" },
          { kind: "thinking_block", text: "must not render as prose" },
        ]),
      ],
      true,
    );
    expect(got.transcript?.turns).toHaveLength(1);
    expect(got.transcript?.turns[0].text).toBe("kept");
    expect(got.unknownTurnKinds).toEqual(["thinking_block"]);
  });

  it("drops an interaction whose type this build does not know", () => {
    const got = readInterpretation([interactionPart({ interaction: "elicitation" })], true);
    expect(got.interaction).toBeNull();
  });

  it("drops a diff line with an unknown op but keeps the known ones", () => {
    const got = readInterpretation(
      [
        interactionPart({
          diff: [
            { line: 1, op: "context", text: "a" },
            { line: 2, op: "rename", text: "b" },
            { line: 3, op: "add", text: "c" },
          ],
        }),
      ],
      true,
    );
    expect(got.interaction?.diff.map((d) => d.op)).toEqual(["context", "add"]);
  });

  it("keeps a tool name only on a tool_call turn", () => {
    const got = readInterpretation(
      [
        transcriptPart([
          { kind: "tool_call", tool: "Bash", text: "ls" },
          { kind: "agent_text", tool: "Bash", text: "prose" },
        ]),
      ],
      true,
    );
    expect(got.transcript?.turns[0].tool).toBe("Bash");
    expect(got.transcript?.turns[1].tool).toBe("");
  });

  it("returns no transcript when every turn was dropped", () => {
    const got = readInterpretation([transcriptPart([{ kind: "mystery", text: "x" }])], true);
    expect(got.transcript).toBeNull();
  });
});

describe("readInterpretation — answerability", () => {
  it("is answerable when the relay says so and every option has a valid key", () => {
    const got = readInterpretation([interactionPart()], true);
    expect(got.interaction?.answerable).toBe(true);
    expect(got.interaction?.options.map((o) => o.sendKey)).toEqual(["1", "2"]);
  });

  it("is not answerable when the relay says so, even with keys present", () => {
    const got = readInterpretation([interactionPart({ answerable: false })], true);
    expect(got.interaction?.answerable).toBe(false);
  });

  // The OpenCode shape: fully described, no key on any option.
  it("surfaces a keyless selection row but refuses to call it answerable", () => {
    const got = readInterpretation(
      [
        interactionPart({
          parser: "opencode",
          answerable: false,
          title: "Edit /tmp/notes.txt",
          question: "Permission required",
          options: [{ label: "Allow once" }, { label: "Allow always" }, { label: "Reject" }],
        }),
      ],
      true,
    );
    expect(got.interaction?.answerable).toBe(false);
    expect(got.interaction?.options).toHaveLength(3);
    expect(got.interaction?.options.every((o) => o.sendKey === null)).toBe(true);
  });

  it("refuses answerable when any option lost its key during normalization", () => {
    // The relay claimed answerable, but one key is invalid. Trusting the flag alone
    // would present a partially-answerable prompt as fully answerable.
    const got = readInterpretation(
      [
        interactionPart({
          answerable: true,
          options: [
            { label: "Yes", send_key: "1" },
            { label: "Maybe", send_key: "enter" },
          ],
        }),
      ],
      true,
    );
    expect(got.interaction?.answerable).toBe(false);
    expect(got.interaction?.options[1].sendKey).toBeNull();
  });
});

describe("validSendKey — the allowlist", () => {
  it("accepts exactly one digit 1-9", () => {
    for (const key of ["1", "5", "9"]) expect(validSendKey(key)).toBe(true);
  });

  it("rejects anything else", () => {
    for (const key of ["0", "10", "", " 1", "1 ", "y", "enter", "ctrl+c", "\r", "1\n", null, undefined, 1]) {
      expect(validSendKey(key)).toBe(false);
    }
  });

  it("never derives a key from a label", () => {
    // A label that looks like a keystroke must not become one.
    const got = readInterpretation(
      [interactionPart({ options: [{ label: "3" }, { label: "rm -rf /" }], answerable: true })],
      true,
    );
    expect(got.interaction?.options.map((o) => o.sendKey)).toEqual([null, null]);
    expect(got.interaction?.answerable).toBe(false);
  });
});

describe("readInterpretation — malformed input", () => {
  it("survives nulls, wrong types, and missing fields", () => {
    const parts = [
      null,
      "nonsense",
      42,
      { type: PART_INTERPRETED_TRANSCRIPT },
      { type: PART_INTERPRETED_INTERACTION },
      transcriptPart([null, 7, { kind: "agent_text" }, { kind: "agent_text", text: "ok" }] as never),
    ] as unknown as WireRunPart[];
    const got = readInterpretation(parts, true);
    expect(got.transcript?.turns.map((t) => t.text)).toEqual(["ok"]);
    expect(got.interaction).toBeNull();
  });

  it("clamps negative dropped counts rather than displaying them", () => {
    const got = readInterpretation(
      [transcriptPart([{ kind: "agent_text", text: "x" }], { dropped_turns: -5, dropped_lines: -1 })],
      true,
    );
    expect(got.transcript?.droppedTurns).toBe(0);
    expect(got.transcript?.droppedLines).toBe(0);
  });

  it("takes only the first part of each interpreted type", () => {
    const got = readInterpretation(
      [
        transcriptPart([{ kind: "agent_text", text: "first" }]),
        transcriptPart([{ kind: "agent_text", text: "second" }]),
      ],
      true,
    );
    expect(got.transcript?.turns).toHaveLength(1);
    expect(got.transcript?.turns[0].text).toBe("first");
  });
});
