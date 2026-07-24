/**
 * UI preferences (theme, terminal font size) — SPEC §8 [ui]. Persisted to
 * localStorage (non-secret only; the CSRF token and session never touch storage,
 * SPEC §9.1). Exposed as a tiny external store for useSyncExternalStore.
 */
export type ThemeSetting = "system" | "dark" | "light";

export interface Prefs {
  theme: ThemeSetting;
  terminalFontSize: number;
}

const KEY = "herdr-phone.prefs";
const DEFAULTS: Prefs = { theme: "system", terminalFontSize: 13 };

function load(): Prefs {
  if (typeof localStorage === "undefined") return { ...DEFAULTS };
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { ...DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<Prefs>;
    return {
      theme: parsed.theme === "dark" || parsed.theme === "light" ? parsed.theme : "system",
      terminalFontSize:
        typeof parsed.terminalFontSize === "number"
          ? Math.min(22, Math.max(9, parsed.terminalFontSize))
          : DEFAULTS.terminalFontSize,
    };
  } catch {
    return { ...DEFAULTS };
  }
}

let state = load();
const listeners = new Set<() => void>();

export const prefsStore = {
  subscribe(cb: () => void): () => void {
    listeners.add(cb);
    return () => listeners.delete(cb);
  },
  get(): Prefs {
    return state;
  },
  set(patch: Partial<Prefs>): void {
    state = { ...state, ...patch };
    try {
      localStorage.setItem(KEY, JSON.stringify(state));
    } catch {
      /* storage unavailable; keep in-memory */
    }
    for (const cb of listeners) cb();
    applyTheme(state.theme);
  },
};

/** Resolve system preference to an effective theme. */
export function effectiveTheme(theme: ThemeSetting): "dark" | "light" {
  if (theme === "system") {
    const prefersLight =
      typeof window !== "undefined" && window.matchMedia
        ? window.matchMedia("(prefers-color-scheme: light)").matches
        : false;
    return prefersLight ? "light" : "dark";
  }
  return theme;
}

/** Apply the theme class to <html>. */
export function applyTheme(theme: ThemeSetting): void {
  if (typeof document === "undefined") return;
  const eff = effectiveTheme(theme);
  const root = document.documentElement;
  root.classList.toggle("light", eff === "light");
  root.classList.toggle("dark", eff === "dark");
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute("content", eff === "light" ? "#eef3f2" : "#101820");
}
