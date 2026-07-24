import type { AgentStatus } from "./types";

/** Human, screen-reader-friendly label for an agent state. */
export function statusLabel(status: AgentStatus): string {
  switch (status) {
    case "blocked":
      return "Needs you";
    case "working":
      return "Working";
    case "idle":
      return "Idle";
    case "done":
      return "Done";
    case "unknown":
      return "Unknown";
  }
}

/** The palette token name a status maps to (SPEC §14.2). */
export function statusTone(status: AgentStatus): "flare" | "tide" | "brass" | "muted" {
  switch (status) {
    case "blocked":
      return "flare";
    case "working":
      return "tide";
    case "done":
      return "brass";
    case "idle":
    case "unknown":
      return "muted";
  }
}

/** Compact "3s ago" / "4m ago" relative time; stable and locale-free. */
export function relativeTime(fromUnixMs: number, nowUnixMs: number): string {
  const delta = Math.max(0, nowUnixMs - fromUnixMs);
  const s = Math.floor(delta / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

/** Shorten an absolute cwd to a phone-friendly tail, keeping the leaf. */
export function shortPath(path: string, maxSegments = 2): string {
  if (!path) return "";
  const home = path.replace(/^\/Users\/[^/]+/, "~").replace(/^\/home\/[^/]+/, "~");
  const parts = home.split("/").filter(Boolean);
  if (parts.length <= maxSegments) return home.startsWith("~") ? home : `/${parts.join("/")}`;
  return `.../${parts.slice(-maxSegments).join("/")}`;
}

/** Strip C0/C1 control characters that must never reach a label or the DOM. */
export function stripControl(text: string): string {
  let out = "";
  for (const ch of text) {
    const code = ch.codePointAt(0) ?? 0;
    const isControl = code <= 0x1f || (code >= 0x7f && code <= 0x9f);
    if (!isControl) out += ch;
  }
  return out;
}
