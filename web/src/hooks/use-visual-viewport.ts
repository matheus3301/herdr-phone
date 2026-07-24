import { useEffect, useState } from "react";
import { keyboardInset, readViewport, type ViewportMetrics } from "@/lib/keyboard-layout";

export interface ViewportState extends ViewportMetrics {
  keyboardInset: number;
}

/**
 * Track the VisualViewport so the control shelf can lift above the software
 * keyboard (SPEC §14.4). Debounced with rAF; never starves the last update.
 */
export function useVisualViewport(): ViewportState {
  const [state, setState] = useState<ViewportState>(() => {
    const m = readViewport();
    return { ...m, keyboardInset: keyboardInset(m) };
  });

  useEffect(() => {
    const vv = window.visualViewport;
    let raf = 0;
    const update = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const m = readViewport();
        setState({ ...m, keyboardInset: keyboardInset(m) });
      });
    };
    update();
    vv?.addEventListener("resize", update);
    vv?.addEventListener("scroll", update);
    window.addEventListener("resize", update);
    return () => {
      cancelAnimationFrame(raf);
      vv?.removeEventListener("resize", update);
      vv?.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
    };
  }, []);

  return state;
}
