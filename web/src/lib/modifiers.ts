/**
 * Tri-state modifier model for the mobile key dock (SPEC §14.4).
 * Each modifier cycles: off -> next (armed for the next key only) -> locked
 * (stays armed across keys and Sends) -> off.
 */

export const MODIFIERS = ["ctrl", "alt", "shift"] as const;
export type Modifier = (typeof MODIFIERS)[number];

export type ModState = "off" | "next" | "locked";

export type ModifierMap = Record<Modifier, ModState>;

export function emptyModifiers(): ModifierMap {
  return { ctrl: "off", alt: "off", shift: "off" };
}

/** Advance one modifier through the tri-state cycle. */
export function cycle(state: ModState): ModState {
  switch (state) {
    case "off":
      return "next";
    case "next":
      return "locked";
    case "locked":
      return "off";
  }
}

export function cycleModifier(map: ModifierMap, mod: Modifier): ModifierMap {
  return { ...map, [mod]: cycle(map[mod]) };
}

/** The modifiers currently armed (either one-shot or locked). */
export function armed(map: ModifierMap): Modifier[] {
  return MODIFIERS.filter((m) => map[m] !== "off");
}

/**
 * After a key is sent, one-shot ("next") modifiers clear; locked ones persist.
 * Called once per emitted key so a locked ctrl survives a burst.
 */
export function afterKey(map: ModifierMap): ModifierMap {
  const out = emptyModifiers();
  for (const m of MODIFIERS) out[m] = map[m] === "locked" ? "locked" : "off";
  return out;
}

/** Explicitly clear every modifier (the dock's Clear affordance). */
export function clearAll(): ModifierMap {
  return emptyModifiers();
}
