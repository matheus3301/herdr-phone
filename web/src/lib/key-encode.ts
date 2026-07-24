/**
 * Encode a logical chord (from the key dock) into terminal input bytes for the
 * interactive controller WebSocket (SPEC §13). xterm-compatible escape sequences.
 * Returns null for a chord we won't send (so callers fail safe rather than send
 * garbage into a live shell).
 */
const ESC = "\x1b";

const SIMPLE: Record<string, string> = {
  enter: "\r",
  tab: "\t",
  esc: ESC,
  space: " ",
  backspace: "\x7f",
  delete: `${ESC}[3~`,
  home: `${ESC}[H`,
  end: `${ESC}[F`,
  pageup: `${ESC}[5~`,
  pagedown: `${ESC}[6~`,
};

const ARROW: Record<string, string> = { up: "A", down: "B", right: "C", left: "D" };

const FKEY: Record<string, string> = {
  f1: `${ESC}OP`,
  f2: `${ESC}OQ`,
  f3: `${ESC}OR`,
  f4: `${ESC}OS`,
  f5: `${ESC}[15~`,
  f6: `${ESC}[17~`,
  f7: `${ESC}[18~`,
  f8: `${ESC}[19~`,
  f9: `${ESC}[20~`,
  f10: `${ESC}[21~`,
  f11: `${ESC}[23~`,
  f12: `${ESC}[24~`,
};

/** CSI modifier code: 1=none,2=shift,3=alt,5=ctrl (+combinations). */
function modCode(ctrl: boolean, alt: boolean, shift: boolean): number {
  return 1 + (shift ? 1 : 0) + (alt ? 2 : 0) + (ctrl ? 4 : 0);
}

export function encodeChord(chord: string): string | null {
  const parts = chord.toLowerCase().split("+");
  const key = parts[parts.length - 1];
  const ctrl = parts.includes("ctrl");
  const alt = parts.includes("alt");
  const shift = parts.includes("shift");

  // Arrows honor modifiers via CSI 1;<mod><letter>.
  if (key in ARROW) {
    if (ctrl || alt || (shift && parts.length > 1)) {
      return `${ESC}[1;${modCode(ctrl, alt, shift)}${ARROW[key]}`;
    }
    return `${ESC}[${ARROW[key]}`;
  }

  if (key === "tab" && shift) return `${ESC}[Z`;

  if (key in FKEY) return FKEY[key];

  if (key in SIMPLE) {
    const base = SIMPLE[key];
    return alt ? ESC + base : base;
  }

  // Single character.
  if ([...key].length === 1) {
    if (ctrl) {
      const c = key.toUpperCase().charCodeAt(0);
      // Ctrl maps A-Z -> 1-26; a few punctuation controls too.
      if (c >= 64 && c <= 95) return String.fromCharCode(c & 0x1f);
      if (key === " ") return "\x00";
      return null;
    }
    if (alt) return ESC + key;
    return key;
  }

  return null;
}

/** Encode a chord to bytes for the terminal socket. */
export function encodeChordBytes(chord: string): Uint8Array | null {
  const s = encodeChord(chord);
  if (s === null) return null;
  return new TextEncoder().encode(s);
}
