import { useState } from "react";
import { ArrowUp, ArrowDown, ArrowLeft, ArrowRight, CornerDownLeft, ClipboardPaste } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  MODIFIERS,
  cycleModifier,
  afterKey,
  emptyModifiers,
  type Modifier,
  type ModifierMap,
} from "@/lib/modifiers";
import { composeChord } from "@/lib/keys";

const MOD_LABEL: Record<Modifier, string> = { ctrl: "Ctrl", alt: "Alt", shift: "Shift" };

/**
 * In-flow special-keys dock (SPEC §14.4). Sits above the composer, never over the
 * terminal. Modifiers are tri-state (off / next / locked). Emitting a key clears
 * one-shot modifiers and keeps locked ones.
 */
export function KeyDock({
  onChord,
  onPaste,
}: {
  onChord: (chord: string) => void;
  /** Send clipboard text into the terminal (the dock's paste affordance). */
  onPaste?: (text: string) => void;
}) {
  const [mods, setMods] = useState<ModifierMap>(emptyModifiers());
  const [pasteError, setPasteError] = useState<string | null>(null);

  function press(key: string) {
    onChord(composeChord(mods, key));
    setMods((m) => afterKey(m));
  }

  async function paste() {
    setPasteError(null);
    const clip = navigator.clipboard;
    if (!clip || typeof clip.readText !== "function") {
      setPasteError("Clipboard access isn't available in this browser.");
      return;
    }
    let text: string;
    try {
      text = await clip.readText();
    } catch (err) {
      // Distinguish an explicit permission denial from other read failures.
      const denied = err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "SecurityError");
      setPasteError(denied ? "Clipboard permission denied — allow paste access and retry." : "Couldn't read the clipboard.");
      return;
    }
    if (!text) {
      setPasteError("Clipboard is empty.");
      return;
    }
    // Raw text is forwarded; the terminal view sanitizes (strips ESC/unsafe
    // controls) and preserves newlines/tabs via xterm's bracketed paste.
    onPaste?.(text);
  }

  const modClass = (state: string) =>
    cn(
      "tabular",
      state === "locked" && "bg-brass text-onaccent border-brass",
      state === "next" && "border-brass text-brass",
    );

  return (
    <div className="flex flex-col gap-1.5 border-t border-seam bg-bulkhead px-2 py-2">
      {pasteError && (
        <p className="px-1 text-[12px] text-flare" role="status" aria-live="polite">
          {pasteError}
        </p>
      )}
      <div className="flex items-center gap-1.5">
        <Button variant="outline" size="key" onClick={() => press("esc")} aria-label="Escape">
          Esc
        </Button>
        {MODIFIERS.map((m) => (
          <Button
            key={m}
            variant="outline"
            size="key"
            aria-pressed={mods[m] !== "off"}
            className={modClass(mods[m])}
            onClick={() => setMods((cur) => cycleModifier(cur, m))}
          >
            {MOD_LABEL[m]}
            {mods[m] === "locked" && <span aria-hidden> ●</span>}
          </Button>
        ))}
        <Button variant="outline" size="key" onClick={() => press("tab")} aria-label="Tab">
          Tab
        </Button>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="outline" size="key" onClick={() => press("up")} aria-label="Arrow up">
            <ArrowUp className="size-4" />
          </Button>
        </div>
      </div>
      <div className="flex items-center gap-1.5">
        <Button
          variant="outline"
          size="key"
          aria-label="Send Ctrl+C interrupt"
          className="tabular text-flare"
          onClick={() => onChord("ctrl+c")}
        >
          ^C
        </Button>
        <Button variant="outline" size="key" onClick={() => press("space")} aria-label="Space">
          Space
        </Button>
        {onPaste && (
          <Button variant="outline" size="key" onClick={() => void paste()} aria-label="Paste from clipboard">
            <ClipboardPaste className="size-4" />
          </Button>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="outline" size="key" onClick={() => press("left")} aria-label="Arrow left">
            <ArrowLeft className="size-4" />
          </Button>
          <Button variant="outline" size="key" onClick={() => press("down")} aria-label="Arrow down">
            <ArrowDown className="size-4" />
          </Button>
          <Button variant="outline" size="key" onClick={() => press("right")} aria-label="Arrow right">
            <ArrowRight className="size-4" />
          </Button>
          <Button variant="default" size="key" onClick={() => press("enter")} aria-label="Enter">
            <CornerDownLeft className="size-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}
