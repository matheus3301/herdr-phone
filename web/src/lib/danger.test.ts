import { describe, it, expect } from "vitest";
import { assessDanger } from "./danger";

describe("danger-pattern detection (advisory)", () => {
  it("flags destructive commands", () => {
    expect(assessDanger("rm -rf /tmp/x").danger).toBe(true);
    expect(assessDanger("sudo reboot").danger).toBe(true);
    expect(assessDanger("git push --force origin main").danger).toBe(true);
    expect(assessDanger("dd if=/dev/zero of=/dev/sda").danger).toBe(true);
    expect(assessDanger("mkfs.ext4 /dev/sdb").danger).toBe(true);
  });

  it("does not trip on look-alike prose", () => {
    expect(assessDanger("assume the sudoku is solved").danger).toBe(false);
    expect(assessDanger("please format the response").danger).toBe(false);
    expect(assessDanger("remove the extra newline").danger).toBe(false);
  });

  it("returns a reason for the first match", () => {
    expect(assessDanger("sudo rm -rf /").reason).toBeTruthy();
  });

  it("is empty for blank input", () => {
    expect(assessDanger("   ").danger).toBe(false);
  });
});
