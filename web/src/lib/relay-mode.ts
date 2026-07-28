/**
 * Which app gate the relay enforces, and how the SPA decides that from the wire
 * (SPEC §9.1, DELIVERY-v0.3.0 §3, §7).
 *
 * Named mode is Access-only: Cloudflare Access is the interactive gate and the
 * relay re-validates the JWT at the origin on every request, so it provisions the
 * app session from that verified identity and `GET /session` returns 200 with a
 * CSRF token and no pairing round-trip. The pairing form must never be shown in
 * that mode — pasting a secret is not the remedy for an Access problem.
 *
 * Quick mode has no edge identity, so the single-use pairing secret stays the one
 * and only app gate and the pairing flow is unchanged.
 *
 * The mode is not a credential, so the last mode this device saw is cached in
 * localStorage (never the cookie, the CSRF token, or a pairing secret — those
 * never touch storage, SPEC §9.1).
 */
import { ApiError } from "./api";

export type RelayMode = "named" | "quick";

/**
 * Read the mode the relay states on the wire — `identity.mode` on `GET /session`
 * and `POST /pair`, `status.mode` on `GET /capabilities`. Only the exact string
 * `"named"` means named mode, so an absent or unrecognized value fails closed to
 * the mode that still demands a pairing secret.
 */
export function relayMode(wire: string | null | undefined): RelayMode {
  return wire === "named" ? "named" : "quick";
}

const KEY = "herdr-phone.relay-mode";

/** Remember the mode this device last observed (non-secret). */
export function rememberRelayMode(mode: RelayMode): void {
  try {
    localStorage.setItem(KEY, mode);
  } catch {
    /* storage unavailable (private mode / quota): the gate falls back to the
       rejection itself, then to pairing */
  }
}

/** The mode this device last observed, or null if it has never seen one. */
export function recalledRelayMode(): RelayMode | null {
  try {
    const raw = localStorage.getItem(KEY);
    return raw === "named" || raw === "quick" ? raw : null;
  } catch {
    return null;
  }
}

/**
 * Infer the mode from a rejected request when no session could be read.
 *
 * The middleware verifies the Access JWT (step 2) *before* it looks at the app
 * session (step 3) and answers a failed origin-side Access check with
 * `access denied` (internal/server/routes.go). Step 2 is a no-op in quick mode,
 * so that exact rejection can only come from a named-mode relay. Every other 401
 * — `no valid session` — looks the same in both modes, so nothing is inferred.
 */
export function relayModeFromRejection(err: unknown): RelayMode | null {
  if (!(err instanceof ApiError) || err.status !== 401) return null;
  return err.message.toLowerCase().includes("access denied") ? "named" : null;
}

/**
 * Decide which gate to show when `GET /session` failed at boot.
 *
 * Named mode is resolved from the rejection when the origin refused the Access
 * token, and otherwise from the mode this device last saw (which covers a
 * transport failure, where the relay says nothing at all).
 *
 * Pairing is the fallback for a mode this device has never established: it is the
 * remedy in quick mode, and offering it to a named-mode operator is only a detour
 * — the reconnect screen links to it either way — whereas withholding it from a
 * quick-mode operator would lock them out.
 */
export function gateAfterSessionFailure(err: unknown): RelayMode {
  return relayModeFromRejection(err) ?? recalledRelayMode() ?? "quick";
}
