import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { TerminalSocket, type TerminalStatus } from "@/lib/terminal-socket";
import { encodeChordBytes } from "@/lib/key-encode";
import { sanitizePaste } from "@/lib/paste";
import { usePrefersReducedMotion } from "@/hooks/use-media-query";
import { Button } from "@/components/ui/button";

export interface TerminalHandle {
  sendText: (text: string) => void;
  sendChord: (chord: string) => void;
  /** Paste clipboard text, preserving newlines/tabs via xterm bracketed paste. */
  paste: (text: string) => void;
  focus: () => void;
  fit: () => void;
}

const THEME = {
  background: "#101820",
  foreground: "#dce7e4",
  cursor: "#e3b341",
  cursorAccent: "#101820",
  selectionBackground: "#50a8a355",
  black: "#101820",
  red: "#f1745e",
  green: "#50a8a3",
  yellow: "#e3b341",
  blue: "#5aa0c8",
  magenta: "#c58fd6",
  cyan: "#50a8a3",
  white: "#dce7e4",
  brightBlack: "#8ba0a6",
  brightRed: "#f1745e",
  brightGreen: "#6cc0bb",
  brightYellow: "#f0c869",
  brightBlue: "#7fbfe0",
  brightMagenta: "#d7abe4",
  brightCyan: "#6cc0bb",
  brightWhite: "#ffffff",
};

/**
 * Read the rendered cell geometry from xterm's render service. This is xterm
 * internals, so it is accessed defensively and falls back to 0 (a documented
 * "unknown" the backend tolerates) when unavailable.
 */
function cellGeometry(term: Terminal): { w: number; h: number } {
  try {
    const svc = (term as unknown as {
      _core?: { _renderService?: { dimensions?: { css?: { cell?: { width?: number; height?: number } } } } };
    })._core?._renderService?.dimensions?.css?.cell;
    const w = Math.round(svc?.width ?? 0);
    const h = Math.round(svc?.height ?? 0);
    if (w > 0 && h > 0) return { w, h };
  } catch {
    /* fall through to zero */
  }
  return { w: 0, h: 0 };
}

interface TerminalViewProps {
  paneId: string;
  generation: number;
  fontSize: number;
  /** Obtain a terminal.takeover confirmation nonce; null if unavailable. */
  onRequestTakeover?: () => Promise<string | null>;
}

/**
 * Interactive terminal (SPEC §13, §16). xterm.js owns all output; bytes are
 * written directly, never via innerHTML/React. Keyed by pane id + generation so a
 * pane replacement remounts.
 */
export const TerminalView = forwardRef<TerminalHandle, TerminalViewProps>(
  ({ paneId, generation, fontSize, onRequestTakeover }, ref) => {
    const hostRef = useRef<HTMLDivElement | null>(null);
    const termRef = useRef<Terminal | null>(null);
    const fitRef = useRef<FitAddon | null>(null);
    const sockRef = useRef<TerminalSocket | null>(null);
    const [status, setStatus] = useState<TerminalStatus>("connecting");
    const [conflict, setConflict] = useState(false);
    const reducedMotion = usePrefersReducedMotion();

    useImperativeHandle(ref, () => ({
      sendText: (text: string) => sockRef.current?.sendInput(text),
      sendChord: (chord: string) => {
        const bytes = encodeChordBytes(chord);
        if (bytes) sockRef.current?.sendInput(bytes);
      },
      paste: (text: string) => {
        const clean = sanitizePaste(text);
        if (!clean) return;
        // term.paste() applies bracketed-paste wrapping when the remote app has
        // enabled it (its DEC 2004 state is reflected in this local mirror), and
        // fires onData → the terminal socket. Multi-line/tab structure is kept.
        termRef.current?.paste(clean);
      },
      focus: () => termRef.current?.focus(),
      fit: () => fitRef.current?.fit(),
    }));

    useEffect(() => {
      const host = hostRef.current;
      if (!host) return;

      const term = new Terminal({
        fontFamily: '"Commit Mono", ui-monospace, monospace',
        fontSize,
        theme: THEME,
        cursorBlink: !reducedMotion,
        allowProposedApi: true,
        scrollback: 5000,
        convertEol: false,
        macOptionIsMeta: true,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host);
      termRef.current = term;
      fitRef.current = fit;

      try {
        fit.fit();
      } catch {
        /* not yet measurable */
      }

      const sock = new TerminalSocket(paneId, term.cols || 80, term.rows || 24, {
        onData: (bytes) => term.write(bytes),
        onControl: (msg) => {
          switch (msg.type) {
            case "terminal.conflict":
              setConflict(true);
              break;
            case "terminal.opened":
              setConflict(false);
              break;
            case "terminal.closed":
              term.write(`\r\n\x1b[38;2;241;116;94m[${msg.reason ?? "closed"}]\x1b[0m\r\n`);
              break;
            case "terminal.resized":
            case "terminal.pong":
              break;
          }
        },
        onStatus: setStatus,
      });
      sockRef.current = sock;
      sock.connect({ expectedGeneration: generation });

      const dataSub = term.onData((data) => sock.sendInput(data));
      const binSub = term.onBinary((data) => sock.sendInput(Uint8Array.from(data, (c) => c.charCodeAt(0))));
      const scrollSub = term.onScroll(() => {
        /* local viewport scroll; the relay owns scrollback via its frames */
      });

      const doFit = () => {
        try {
          fit.fit();
          const cell = cellGeometry(term);
          sock.resize(term.cols, term.rows, cell.w, cell.h);
        } catch {
          /* ignore transient measure errors */
        }
      };

      let raf = 0;
      const scheduleFit = () => {
        cancelAnimationFrame(raf);
        raf = requestAnimationFrame(doFit);
      };
      const ro = new ResizeObserver(scheduleFit);
      ro.observe(host);
      const vv = window.visualViewport;
      vv?.addEventListener("resize", scheduleFit);
      window.addEventListener("orientationchange", scheduleFit);

      return () => {
        cancelAnimationFrame(raf);
        ro.disconnect();
        vv?.removeEventListener("resize", scheduleFit);
        window.removeEventListener("orientationchange", scheduleFit);
        dataSub.dispose();
        binSub.dispose();
        scrollSub.dispose();
        sock.close();
        term.dispose();
        termRef.current = null;
        fitRef.current = null;
        sockRef.current = null;
      };
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [paneId, generation]);

    useEffect(() => {
      const term = termRef.current;
      if (!term) return;
      term.options.fontSize = fontSize;
      try {
        fitRef.current?.fit();
      } catch {
        /* ignore */
      }
    }, [fontSize]);

    async function takeover() {
      const confirmation = onRequestTakeover ? await onRequestTakeover() : null;
      if (!confirmation) return; // read-only or preparation failed
      setConflict(false);
      sockRef.current?.connect({ takeover: true, confirmation, expectedGeneration: generation });
    }

    return (
      <div className="relative flex h-full min-h-0 w-full flex-col bg-terminal">
        <div
          ref={hostRef}
          className="min-h-0 flex-1"
          data-testid="terminal-host"
          role="application"
          aria-label={`Terminal for pane ${paneId}`}
        />
        {(status === "reconnecting" || status === "connecting") && !conflict && (
          <div className="pointer-events-none absolute left-1/2 top-2 -translate-x-1/2 rounded-full border border-frame bg-bulkhead px-3 py-1 font-utility text-[11px] text-muted-ink">
            {status === "connecting" ? "attaching…" : "reattaching…"}
          </div>
        )}
        {conflict && (
          <div className="absolute inset-x-2 top-2 rounded-[10px] border border-flare/50 bg-bulkhead p-3">
            <p className="text-sm text-mist">Another controller owns this terminal.</p>
            <p className="mt-0.5 text-[13px] text-muted-ink">
              Only one controller can drive input at a time. Take over to seize control.
            </p>
            <div className="mt-2 flex justify-end">
              <Button variant="danger" size="sm" onClick={() => void takeover()}>
                Take over
              </Button>
            </div>
          </div>
        )}
      </div>
    );
  },
);
TerminalView.displayName = "TerminalView";
