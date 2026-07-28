/**
 * The boot gate (DELIVERY-v0.3.0 §7): which screen the operator meets before the
 * app shell. The pairing form is quick mode's gate ONLY. In named mode the relay
 * auto-provisions the session from the verified Access identity, so a successful
 * GET /session must go straight to the shell, and a rejected one must offer the
 * Access remedy (reload) rather than a pairing secret that cannot help.
 *
 * `@/router` is mocked to a single sentinel route so the gate can be tested
 * without mounting the whole shell (and the terminal stack it pulls in).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as api from "@/lib/api";
import { store } from "@/lib/store";
import { makeNamedSessionResponse, makeSessionResponse } from "@/test/fixtures";

vi.mock("@/router", async () => {
  const { createBrowserRouter } = await import("react-router-dom");
  return { router: createBrowserRouter([{ path: "*", element: <div>app shell</div> }]) };
});

const { App } = await import("./App");

/** Non-opening WebSocket so store.start() can run under jsdom. */
class IdleWS {
  static OPEN = 1;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  close() {}
  send() {}
}

function stubCaps(mode: "named" | "quick") {
  vi.spyOn(api, "getCapabilities").mockResolvedValue({
    version: 1,
    operations: [],
    capabilities: { herdr_version: "0.7.5", herdr_protocol: 17, live_handoff: true, agent_kinds: [] },
    status: { version: "0.3.0", protocol: 17, mode, ready: true, herdr: { healthy: true }, state: { healthy: true }, clients: 1 },
    tunnel: { mode, public_url: "", health: { healthy: true } },
    limits: { max_body_bytes: 1, max_pane_read_lines: 1, confirmation_ttl_seconds: 30 },
  });
}

const pairingInput = () => screen.queryByLabelText(/pairing link or secret/i);

beforeEach(() => {
  localStorage.clear();
  window.history.replaceState(null, "", "/");
  vi.stubGlobal("WebSocket", IdleWS as unknown as typeof WebSocket);
  vi.spyOn(api, "getSnapshot").mockRejectedValue(new api.ApiError(0, "network", "no snapshot in this test", true));
});

afterEach(() => {
  store.stop();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("boot gate — named mode", () => {
  it("goes straight to the app shell, never showing the pairing form", async () => {
    const pair = vi.spyOn(api, "pair");
    vi.spyOn(api, "getSession").mockResolvedValue(makeNamedSessionResponse());
    stubCaps("named");

    render(<App />);

    expect(await screen.findByText("app shell")).toBeInTheDocument();
    expect(pairingInput()).not.toBeInTheDocument();
    expect(pair).not.toHaveBeenCalled();
    expect(store.canMutate()).toBe(true);
  });

  it("shows the Access reconnect screen — not the pairing form — when the token is refused", async () => {
    vi.spyOn(api, "getSession").mockRejectedValue(new api.ApiError(401, "unauthorized", "access denied"));
    stubCaps("named");

    render(<App />);

    expect(await screen.findByRole("heading", { name: /sign in to continue/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload and sign in/i })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("access denied");
    expect(pairingInput()).not.toBeInTheDocument();
  });

  it("keeps a pairing escape hatch for a re-bind or a reconfigured relay", async () => {
    vi.spyOn(api, "getSession").mockRejectedValue(new api.ApiError(401, "unauthorized", "access denied"));
    stubCaps("named");

    render(<App />);
    await screen.findByRole("heading", { name: /sign in to continue/i });
    await userEvent.click(screen.getByRole("button", { name: /use a pairing link instead/i }));

    expect(pairingInput()).toBeInTheDocument();
  });

  it("sends a pairing attempt the origin refused for Access to the reconnect screen", async () => {
    window.history.replaceState(null, "", "/#pair=some-secret");
    vi.spyOn(api, "pair").mockRejectedValue(new api.ApiError(401, "unauthorized", "access denied"));
    stubCaps("named");

    render(<App />);

    expect(await screen.findByRole("heading", { name: /sign in to continue/i })).toBeInTheDocument();
    expect(pairingInput()).not.toBeInTheDocument();
  });

  it("uses the remembered mode when the relay answers nothing at all", async () => {
    // A previous visit established named mode; now the request never lands, so the
    // rejection carries no mode of its own.
    localStorage.setItem("herdr-phone.relay-mode", "named");
    vi.spyOn(api, "getSession").mockRejectedValue(new api.ApiError(0, "network", "Could not reach the relay.", true));
    stubCaps("named");

    render(<App />);

    expect(await screen.findByRole("heading", { name: /sign in to continue/i })).toBeInTheDocument();
    expect(pairingInput()).not.toBeInTheDocument();
  });
});

describe("boot gate — quick mode", () => {
  it("shows the pairing form when there is no session", async () => {
    vi.spyOn(api, "getSession").mockRejectedValue(new api.ApiError(401, "unauthorized", "no valid session"));
    stubCaps("quick");

    render(<App />);

    expect(await screen.findByLabelText(/pairing link or secret/i)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /sign in to continue/i })).not.toBeInTheDocument();
  });

  it("still pairs from a #pair= fragment and consumes it from the URL", async () => {
    window.history.replaceState(null, "", "/#pair=dev-secret");
    const pair = vi.spyOn(api, "pair").mockResolvedValue(makeSessionResponse({ csrf_token: "paired-csrf" }));
    vi.spyOn(api, "getSession").mockResolvedValue(makeSessionResponse({ csrf_token: "paired-csrf" }));
    stubCaps("quick");

    render(<App />);

    expect(await screen.findByText("app shell")).toBeInTheDocument();
    expect(pair).toHaveBeenCalledWith("dev-secret");
    expect(window.location.hash).toBe("");
  });

  it("pairs from a pasted link on the pairing form", async () => {
    vi.spyOn(api, "getSession")
      .mockRejectedValueOnce(new api.ApiError(401, "unauthorized", "no valid session"))
      .mockResolvedValue(makeSessionResponse({ csrf_token: "paired-csrf" }));
    const pair = vi.spyOn(api, "pair").mockResolvedValue(makeSessionResponse({ csrf_token: "paired-csrf" }));
    stubCaps("quick");

    render(<App />);
    await userEvent.type(await screen.findByLabelText(/pairing link or secret/i), "https://host/#pair=xyz");
    await userEvent.click(screen.getByRole("button", { name: /pair device/i }));

    await waitFor(() => expect(pair).toHaveBeenCalledWith("xyz"));
    expect(await screen.findByText("app shell")).toBeInTheDocument();
  });

  it("keeps the pairing form when a #pair= fragment is rejected", async () => {
    window.history.replaceState(null, "", "/#pair=stale-secret");
    vi.spyOn(api, "pair").mockRejectedValue(new api.ApiError(401, "unauthorized", "pairing rejected"));
    stubCaps("quick");

    render(<App />);

    expect(await screen.findByLabelText(/pairing link or secret/i)).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("pairing rejected");
    // Even a remembered named mode must not swallow a genuine pairing error.
    expect(screen.queryByRole("heading", { name: /sign in to continue/i })).not.toBeInTheDocument();
  });

  it("keeps the pairing form when the pasted secret is rejected", async () => {
    vi.spyOn(api, "getSession").mockRejectedValue(new api.ApiError(401, "unauthorized", "no valid session"));
    vi.spyOn(api, "pair").mockRejectedValue(new api.ApiError(401, "unauthorized", "pairing rejected"));
    stubCaps("quick");

    render(<App />);
    await userEvent.type(await screen.findByLabelText(/pairing link or secret/i), "bad-secret");
    await userEvent.click(screen.getByRole("button", { name: /pair device/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent("pairing rejected");
    expect(pairingInput()).toBeInTheDocument();
  });
});
