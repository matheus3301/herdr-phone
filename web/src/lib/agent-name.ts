/**
 * Agent-name rules mirrored from the backend (internal/herdr/agents.go
 * ValidAgentName): `^[a-z][a-z0-9_-]{0,31}$`, max 32 chars, unique among live
 * agents. The Start Agent flow validates and suggests names against this so a
 * malformed/duplicate name never reaches Herdr (which rejects invalid_params).
 */
export const AGENT_NAME_MAX = 32;

export function isValidAgentName(name: string): boolean {
  if (!name || name.length > AGENT_NAME_MAX) return false;
  return /^[a-z][a-z0-9_-]*$/.test(name);
}

/** Coerce an arbitrary label toward a valid agent name (lowercase, strip). */
export function sanitizeAgentName(raw: string): string {
  const s = raw
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, AGENT_NAME_MAX);
  return s;
}

/** Suggest a unique valid name from a kind, avoiding names already in use. */
export function suggestAgentName(kind: string, existing: readonly string[]): string {
  const base = sanitizeAgentName(kind) || "agent";
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let n = 2; n < 1000; n++) {
    const candidate = `${base}-${n}`.slice(0, AGENT_NAME_MAX);
    if (!taken.has(candidate)) return candidate;
  }
  return base;
}
