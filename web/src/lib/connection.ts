/**
 * Reconnect vocabulary.
 *
 * "Offline" is six different problems with six different recoveries, and
 * collapsing them into one banner leaves the operator guessing. The relay's
 * health clock reports link state; the run and console views add the reasons
 * only they can know (the pane was replaced, the agent ended, another
 * controller owns the terminal).
 */
import type { ConnectionState } from "./store";

export type ConnectionReason =
  | "phone-offline"
  | "relay-reconnecting"
  | "host-unavailable"
  | "pane-replaced"
  | "agent-ended"
  | "console-conflict";

export interface ConnectionMessage {
  title: string;
  detail: string;
  tone: "warning" | "danger";
}

export const CONNECTION_MESSAGES: Record<ConnectionReason, ConnectionMessage> = {
  "phone-offline": {
    title: "This phone is offline",
    detail: "Your device has no network. Herdr keeps running on your Mac.",
    tone: "warning",
  },
  "relay-reconnecting": {
    title: "Reconnecting to the relay",
    detail: "The link dropped and is being re-established. Recent state may be a few seconds old.",
    tone: "warning",
  },
  "host-unavailable": {
    title: "Can't reach your Mac",
    detail: "The tunnel or the Herdr session is down. Check both, then retry.",
    tone: "danger",
  },
  "pane-replaced": {
    title: "This pane was replaced",
    detail: "Herdr recycled the pane, so this run is frozen. Open the pane's current occupant to continue.",
    tone: "danger",
  },
  "agent-ended": {
    title: "This agent has ended",
    detail: "No agent occupies the pane any more. Start a new run or open the console.",
    tone: "danger",
  },
  "console-conflict": {
    title: "Another controller owns this console",
    detail: "Only one controller can drive input. Taking over requires an explicit confirmation.",
    tone: "danger",
  },
};

/**
 * Map link health to a reason. `navigator.onLine` is advisory only — it is used
 * to *name* an already-detected failure, never to gate a request.
 */
export function reasonFor(connection: ConnectionState, online: boolean): ConnectionReason | null {
  if (connection === "live" || connection === "connecting") return null;
  if (!online) return "phone-offline";
  return connection === "lost" ? "host-unavailable" : "relay-reconnecting";
}
