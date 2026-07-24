/**
 * Advisory danger-pattern detection for free-text sent to a terminal or agent
 * (SPEC §14.4). This is a second-tap warning, NOT a sandbox and NOT a server
 * nonce — the shell is authorized and we never pretend otherwise. Patterns are
 * word-boundary anchored so ordinary prose ("assume", "sudoku") never trips.
 */

interface DangerPattern {
  test: RegExp;
  reason: string;
}

const PATTERNS: DangerPattern[] = [
  { test: /\brm\s+-[a-z]*r[a-z]*f?|\brm\s+-[a-z]*f[a-z]*r?/i, reason: "recursive/forced delete (rm -rf)" },
  { test: /\bsudo\b/i, reason: "elevated privileges (sudo)" },
  { test: /\bgit\s+push\b.*(--force\b|-f\b)/i, reason: "force push (git push --force)" },
  { test: /(^|\s)--force(\s|$)/i, reason: "forced operation (--force)" },
  { test: /\bdd\s+if=/i, reason: "raw disk write (dd if=)" },
  { test: /\bmkfs(\.\w+)?\b/i, reason: "filesystem format (mkfs)" },
  { test: /\bgit\s+reset\s+--hard\b/i, reason: "discard changes (git reset --hard)" },
  { test: />\s*\/(etc|dev|sys|proc|boot|usr\/bin)\b/i, reason: "redirect into a system path" },
  { test: /\bshutdown\b|\breboot\b|\bhalt\b/i, reason: "host power state change" },
];

export interface DangerVerdict {
  danger: boolean;
  reason: string | null;
}

/** Returns the first, most-specific danger reason for a line, if any. */
export function assessDanger(text: string): DangerVerdict {
  const line = text.trim();
  if (!line) return { danger: false, reason: null };
  for (const p of PATTERNS) {
    if (p.test.test(line)) return { danger: true, reason: p.reason };
  }
  return { danger: false, reason: null };
}
