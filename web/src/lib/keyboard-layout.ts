/**
 * Software-keyboard + safe-area layout math (SPEC §14.4).
 * Controls must sit in document flow above the on-screen keyboard, never over
 * the terminal. We derive an "inset from the bottom" from the VisualViewport so
 * the control shelf can be lifted exactly by the keyboard height.
 */

export interface ViewportMetrics {
  /** Full layout viewport height (px). */
  layoutHeight: number;
  /** Visible viewport height with the keyboard shown (px). */
  visualHeight: number;
  /** How far the visual viewport is offset from the top (px). */
  offsetTop: number;
}

/**
 * Keyboard overlap in CSS px: the part of the layout viewport currently hidden
 * by the software keyboard. Clamped to >= 0 and ignores tiny rounding noise.
 */
export function keyboardInset(m: ViewportMetrics): number {
  const overlap = m.layoutHeight - (m.visualHeight + m.offsetTop);
  if (overlap < 24) return 0; // browser chrome / rounding, not a keyboard
  return Math.round(overlap);
}

/** Whether the software keyboard is (probably) open. */
export function keyboardOpen(m: ViewportMetrics): boolean {
  return keyboardInset(m) > 0;
}

/** Read current metrics from the live VisualViewport, with sane fallbacks. */
export function readViewport(win: Window = window): ViewportMetrics {
  const vv = win.visualViewport;
  const layoutHeight = win.innerHeight || (vv ? vv.height : 0);
  return {
    layoutHeight,
    visualHeight: vv ? vv.height : layoutHeight,
    offsetTop: vv ? vv.offsetTop : 0,
  };
}
