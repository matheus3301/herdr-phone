import type { Modifier, ModifierMap } from "./modifiers";
import { armed } from "./modifiers";

/**
 * Logical key model. The relay validates keys before writing bytes to Herdr
 * (Herdr `pane.send_keys` grammar, docs/research/herdr-contract.md §6c), so the
 * client only emits names from this validated set and composes `+`-joined chords.
 */

export const BASE_KEYS = [
  "enter",
  "tab",
  "esc",
  "backspace",
  "delete",
  "up",
  "down",
  "left",
  "right",
  "home",
  "end",
  "pageup",
  "pagedown",
  "space",
] as const;

export type BaseKey = (typeof BASE_KEYS)[number];

const FUNCTION_KEYS = new Set(
  Array.from({ length: 12 }, (_, i) => `f${i + 1}`),
);

const MODIFIER_ORDER: Modifier[] = ["ctrl", "alt", "shift"];

/** Validate a single logical key token the relay will accept. */
export function isValidBaseKey(name: string): boolean {
  const k = name.toLowerCase();
  if ((BASE_KEYS as readonly string[]).includes(k)) return true;
  if (FUNCTION_KEYS.has(k)) return true;
  // A single printable character is a literal key.
  if ([...k].length === 1 && k >= " ") return true;
  return false;
}

/** Compose a chord string from armed modifiers + a base key, in canonical order. */
export function composeChord(mods: ModifierMap, key: string): string {
  const active = new Set(armed(mods));
  const parts: string[] = MODIFIER_ORDER.filter((m) => active.has(m));
  parts.push(key.toLowerCase());
  return parts.join("+");
}

/** Validate a fully composed chord (e.g. "ctrl+shift+p"). */
export function isValidChord(chord: string): boolean {
  const parts = chord.split("+");
  if (parts.length === 0) return false;
  const key = parts[parts.length - 1];
  const mods = parts.slice(0, -1);
  for (const m of mods) {
    if (!MODIFIER_ORDER.includes(m as Modifier)) return false;
  }
  // No duplicate modifiers.
  if (new Set(mods).size !== mods.length) return false;
  return isValidBaseKey(key);
}

/** Danger keys warned about (interrupt/EOF/suspend) in the dock. */
export function isDangerChord(chord: string): boolean {
  return ["ctrl+c", "ctrl+d", "ctrl+z", "ctrl+\\"].includes(chord.toLowerCase());
}

/** A short human caption for a chord, used in the key-queue strip. */
export function chordCaption(chord: string): string {
  return chord
    .split("+")
    .map((p) => (p.length === 1 ? p.toUpperCase() : p[0].toUpperCase() + p.slice(1)))
    .join("+");
}
