/**
 * Sanitize clipboard text for a terminal paste (SPEC §14.4, review F1).
 *
 * Unlike stripControl (used for labels), a paste must PRESERVE structure —
 * newlines and tabs carry command semantics — while still removing bytes that
 * could break out of the paste or drive the remote application: ESC (0x1b) and
 * every other C0/C1 control. The cleaned text is then handed to xterm's own
 * `paste()`, which wraps it in a bracketed-paste sequence when the remote app has
 * enabled DEC mode 2004, so a multi-line block is delivered as one literal paste
 * instead of a run of executed commands (and never silently concatenated).
 */
export function sanitizePaste(text: string): string {
  // Normalize CRLF / lone CR to LF so newline handling is consistent; xterm's
  // paste converts LF back to the terminal's expected CR.
  const normalized = text.replace(/\r\n?/g, "\n");
  let out = "";
  for (const ch of normalized) {
    const code = ch.codePointAt(0) ?? 0;
    if (ch === "\n" || ch === "\t") {
      out += ch;
      continue;
    }
    // Drop ESC and all other C0 (0x00–0x1F) and C1 (0x7F–0x9F) controls.
    if (code <= 0x1f || (code >= 0x7f && code <= 0x9f)) continue;
    out += ch;
  }
  return out;
}
