import { describe, it, expect, beforeEach } from "vitest";
import { ApiError } from "./api";
import {
  gateAfterSessionFailure,
  recalledRelayMode,
  relayMode,
  relayModeFromRejection,
  rememberRelayMode,
} from "./relay-mode";

describe("relayMode — the wire value", () => {
  it("reads only the exact string 'named' as named mode", () => {
    expect(relayMode("named")).toBe("named");
    expect(relayMode("quick")).toBe("quick");
  });

  it("fails closed to quick for an absent or unrecognized value", () => {
    expect(relayMode(undefined)).toBe("quick");
    expect(relayMode(null)).toBe("quick");
    expect(relayMode("")).toBe("quick");
    expect(relayMode("Named")).toBe("quick");
    expect(relayMode("access")).toBe("quick");
  });
});

describe("relayModeFromRejection", () => {
  it("reads a refused Access token as named mode", () => {
    // Middleware step 2 rejects before it ever looks at the app session, and it is
    // a no-op in quick mode, so only a named-mode relay can answer this way.
    expect(relayModeFromRejection(new ApiError(401, "unauthorized", "access denied"))).toBe("named");
  });

  it("infers nothing from a missing-session 401 (both modes emit it)", () => {
    expect(relayModeFromRejection(new ApiError(401, "unauthorized", "no valid session"))).toBeNull();
  });

  it("infers nothing from a non-401 or a non-API failure", () => {
    expect(relayModeFromRejection(new ApiError(403, "forbidden", "access denied"))).toBeNull();
    expect(relayModeFromRejection(new ApiError(0, "network", "Could not reach the relay.", true))).toBeNull();
    expect(relayModeFromRejection(new Error("access denied"))).toBeNull();
    expect(relayModeFromRejection(null)).toBeNull();
  });
});

describe("gateAfterSessionFailure", () => {
  beforeEach(() => localStorage.clear());

  it("shows the named reconnect gate when the origin refused the Access token", () => {
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "access denied"))).toBe("named");
  });

  it("shows the pairing gate for a first-ever boot with no mode to go on", () => {
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "no valid session"))).toBe("quick");
  });

  it("falls back to the mode this device last established", () => {
    rememberRelayMode("named");
    expect(recalledRelayMode()).toBe("named");
    // A transport failure carries no mode signal at all.
    expect(gateAfterSessionFailure(new ApiError(0, "network", "Could not reach the relay.", true))).toBe("named");
    // Nor does an ambiguous 401.
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "no valid session"))).toBe("named");
  });

  it("keeps the pairing gate for a remembered quick-mode relay", () => {
    rememberRelayMode("quick");
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "no valid session"))).toBe("quick");
  });

  it("prefers the relay's own rejection over a stale memory", () => {
    rememberRelayMode("quick");
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "access denied"))).toBe("named");
  });

  it("ignores a corrupted stored value", () => {
    localStorage.setItem("herdr-phone.relay-mode", "wide-open");
    expect(recalledRelayMode()).toBeNull();
    expect(gateAfterSessionFailure(new ApiError(401, "unauthorized", "no valid session"))).toBe("quick");
  });
});
