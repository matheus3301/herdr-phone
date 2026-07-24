import { describe, it, expect, vi } from "vitest";
import { Backoff, onRevalidate } from "./reconnect";

describe("Backoff", () => {
  it("grows exponentially and caps at maxMs", () => {
    const b = new Backoff({ baseMs: 100, factor: 2, maxMs: 1000, jitter: 0, rng: () => 0.5 });
    expect(b.next()).toBe(100);
    expect(b.next()).toBe(200);
    expect(b.next()).toBe(400);
    expect(b.next()).toBe(800);
    expect(b.next()).toBe(1000);
    expect(b.next()).toBe(1000);
  });

  it("applies symmetric jitter within bounds", () => {
    const b = new Backoff({ baseMs: 1000, factor: 1, maxMs: 1000, jitter: 0.25, rng: () => 1 });
    // rng()=1 -> +25%
    expect(b.next()).toBe(1250);
    const b2 = new Backoff({ baseMs: 1000, factor: 1, maxMs: 1000, jitter: 0.25, rng: () => 0 });
    expect(b2.next()).toBe(750);
  });

  it("resets the attempt counter", () => {
    const b = new Backoff({ baseMs: 100, factor: 2, maxMs: 9999, jitter: 0, rng: () => 0.5 });
    b.next();
    b.next();
    b.reset();
    expect(b.next()).toBe(100);
  });
});

describe("onRevalidate", () => {
  it("fires on pageshow/focus/online and on visibility when visible", () => {
    const cb = vi.fn();
    const dispose = onRevalidate(cb);

    window.dispatchEvent(new Event("focus"));
    expect(cb).toHaveBeenCalledTimes(1);

    window.dispatchEvent(new Event("online"));
    expect(cb).toHaveBeenCalledTimes(2);

    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    expect(cb).toHaveBeenCalledTimes(3);

    // freeze + resume are registered per SPEC §16 (L8).
    document.dispatchEvent(new Event("freeze"));
    expect(cb).toHaveBeenCalledTimes(4);
    document.dispatchEvent(new Event("resume"));
    expect(cb).toHaveBeenCalledTimes(5);

    dispose();
    window.dispatchEvent(new Event("focus"));
    expect(cb).toHaveBeenCalledTimes(5);
  });
});
