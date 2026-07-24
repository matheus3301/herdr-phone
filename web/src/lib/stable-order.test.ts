import { describe, it, expect } from "vitest";
import { stabilizeGroups } from "./stable-order";

type Row = { id: string; status: string };
type Group = { key: string; runs: Row[] };

const groups = (attention: Row[], working: Row[]): Group[] => [
  { key: "attention", runs: attention },
  { key: "working", runs: working },
];

describe("stabilizeGroups", () => {
  it("passes the live order through when nothing is frozen", () => {
    const live = groups([{ id: "a", status: "blocked" }], []);
    expect(stabilizeGroups(null, live)).toEqual(live);
    expect(stabilizeGroups([], live)).toEqual(live);
  });

  it("keeps a row in its frozen position while the list is being touched", () => {
    const frozen = groups([], [
      { id: "a", status: "working" },
      { id: "b", status: "working" },
    ]);
    const live = groups([], [
      { id: "b", status: "working" },
      { id: "a", status: "working" },
    ]);
    expect(stabilizeGroups(frozen, live)[1].runs.map((r) => r.id)).toEqual(["a", "b"]);
  });

  it("keeps a row in its frozen section even when its status changes", () => {
    // The row that just started needing a decision must not leap into another
    // section under the operator's finger.
    const frozen = groups([], [{ id: "a", status: "working" }]);
    const live = groups([{ id: "a", status: "blocked" }], []);
    const held = stabilizeGroups(frozen, live);
    expect(held[0].runs).toEqual([]);
    expect(held[1].runs).toEqual([{ id: "a", status: "blocked" }]);
  });

  it("still refreshes a held row's content in place", () => {
    const frozen = groups([], [{ id: "a", status: "working" }]);
    const live = groups([], [{ id: "a", status: "done" }]);
    expect(stabilizeGroups(frozen, live)[1].runs[0].status).toBe("done");
  });

  it("appends genuinely new rows to the end of their live section", () => {
    const frozen = groups([{ id: "a", status: "blocked" }], []);
    const live = groups(
      [
        { id: "z", status: "blocked" },
        { id: "a", status: "blocked" },
      ],
      [{ id: "n", status: "working" }],
    );
    const held = stabilizeGroups(frozen, live);
    expect(held[0].runs.map((r) => r.id)).toEqual(["a", "z"]);
    expect(held[1].runs.map((r) => r.id)).toEqual(["n"]);
  });

  it("drops rows that disappeared", () => {
    const frozen = groups([], [
      { id: "a", status: "working" },
      { id: "b", status: "working" },
    ]);
    const live = groups([], [{ id: "b", status: "working" }]);
    expect(stabilizeGroups(frozen, live)[1].runs.map((r) => r.id)).toEqual(["b"]);
  });

  it("never duplicates a row that moved section", () => {
    const frozen = groups([{ id: "a", status: "blocked" }], [{ id: "a", status: "working" }]);
    const live = groups([{ id: "a", status: "blocked" }], []);
    const ids = stabilizeGroups(frozen, live).flatMap((g) => g.runs.map((r) => r.id));
    expect(ids).toEqual(["a"]);
  });

  it("does not mutate the frozen reference", () => {
    const frozen = groups([{ id: "a", status: "blocked" }], []);
    stabilizeGroups(frozen, groups([{ id: "a", status: "blocked" }], [{ id: "n", status: "working" }]));
    expect(frozen[1].runs).toEqual([]);
  });
});
