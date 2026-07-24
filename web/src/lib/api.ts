/**
 * Typed relay REST client (SPEC §12) — reconciled to the Go backend. Same-origin
 * only; credentials ride the HttpOnly session cookie, and mutating requests carry
 * the CSRF header the pair endpoint issued. Returns wire shapes; lib/normalize.ts
 * maps them to view models.
 */
import type {
  ConfirmationRequest,
  ConfirmationResult,
  DirectoryListing,
  MutationRequest,
  MutationResponse,
  WireCapabilities,
  WireConfirmationResponse,
  WireDirectoriesResponse,
  WireMutationResponse,
  WirePaneReadResponse,
  WirePairResponse,
  WireSessionResponse,
  WireSnapshotEnvelope,
  ReadSource,
} from "./types";

export const API_BASE = "/api/v1";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryable = false,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  csrfToken?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
  headers?: Record<string, string>;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<{ data: T; res: Response }> {
  const method = opts.method ?? "GET";
  const controller = new AbortController();
  const timeout = opts.timeoutMs ?? 8000;
  const timer = setTimeout(() => controller.abort(new DOMException("timeout", "AbortError")), timeout);
  if (opts.signal) {
    if (opts.signal.aborted) controller.abort();
    else opts.signal.addEventListener("abort", () => controller.abort(), { once: true });
  }

  const headers: Record<string, string> = { ...opts.headers };
  const isMutating = method !== "GET" && method !== "HEAD";
  if (isMutating && opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (isMutating) {
    // A custom header is itself part of the CSRF defense (SPEC §9.3).
    headers["X-Requested-With"] = "herdr-phone";
    if (opts.csrfToken) headers["X-CSRF-Token"] = opts.csrfToken;
  }

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      credentials: "same-origin",
      cache: "no-store",
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: controller.signal,
    });
  } catch (err) {
    clearTimeout(timer);
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new ApiError(0, "timeout", "The relay did not respond in time.", true);
    }
    throw new ApiError(0, "network", "Could not reach the relay.", true);
  }
  clearTimeout(timer);

  if (res.status === 304 || res.status === 204) {
    return { data: undefined as T, res };
  }
  if (!res.ok) {
    let code = `http_${res.status}`;
    let message = res.statusText || "Request failed";
    let retryable = res.status >= 500 || res.status === 429;
    try {
      const payload = (await res.json()) as { error?: { code?: string; message?: string; retryable?: boolean } };
      if (payload?.error) {
        code = payload.error.code ?? code;
        message = payload.error.message ?? message;
        retryable = payload.error.retryable ?? retryable;
      }
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, code, message, retryable);
  }

  const text = await res.text();
  const data = (text ? JSON.parse(text) : undefined) as T;
  return { data, res };
}

/* ------------------------------------------------------------------ routes */

export async function pair(secret: string): Promise<WirePairResponse> {
  const { data } = await request<WirePairResponse>("/pair", { method: "POST", body: { secret }, timeoutMs: 12_000 });
  return data;
}

export async function getSession(): Promise<WireSessionResponse> {
  // Carries csrf_token + expiry so a cold reload recovers a mutable session.
  const { data } = await request<WireSessionResponse>("/session");
  return data;
}

export async function endSession(csrfToken: string): Promise<void> {
  await request<void>("/session", { method: "DELETE", csrfToken });
}

export interface SnapshotResult {
  snapshot: WireSnapshotEnvelope | undefined;
  etag: string | null;
  notModified: boolean;
}

export async function getSnapshot(etag: string | null, signal?: AbortSignal): Promise<SnapshotResult> {
  const headers: Record<string, string> = {};
  if (etag) headers["If-None-Match"] = etag;
  const { data, res } = await request<WireSnapshotEnvelope>("/snapshot", { headers, signal, timeoutMs: 8000 });
  return {
    snapshot: res.status === 304 ? undefined : data,
    etag: res.headers.get("ETag"),
    notModified: res.status === 304,
  };
}

export async function getCapabilities(): Promise<WireCapabilities> {
  const { data } = await request<WireCapabilities>("/capabilities");
  return data;
}

export async function readPane(
  paneId: string,
  source: ReadSource,
  lines: number,
  signal?: AbortSignal,
): Promise<WirePaneReadResponse> {
  const q = new URLSearchParams({ source, lines: String(lines) });
  const { data } = await request<WirePaneReadResponse>(
    `/panes/${encodeURIComponent(paneId)}/read?${q.toString()}`,
    { signal },
  );
  return data;
}

/** Parent-directory derivation: the backend response has no `parent`, so the
 * client computes a candidate; the backend still enforces the root confinement. */
function parentOf(path: string): string | null {
  const trimmed = path.replace(/\/+$/, "");
  const idx = trimmed.lastIndexOf("/");
  if (idx <= 0) return trimmed === "" ? null : "/";
  const parent = trimmed.slice(0, idx);
  return parent === path ? null : parent;
}

export async function listDirectories(path: string): Promise<DirectoryListing> {
  const q = new URLSearchParams({ path });
  const { data } = await request<WireDirectoriesResponse>(`/directories?${q.toString()}`);
  return { path: data.path, parent: parentOf(data.path), entries: data.entries };
}

export async function prepareConfirmation(body: ConfirmationRequest, csrfToken: string): Promise<ConfirmationResult> {
  const { data } = await request<WireConfirmationResponse>("/confirmations", { method: "POST", body, csrfToken });
  return { confirmation: data.confirmation, expiresUnixMs: data.expires_unix_ms };
}

export async function mutate(body: MutationRequest, csrfToken: string): Promise<MutationResponse> {
  const { data } = await request<WireMutationResponse>("/mutations", {
    method: "POST",
    body,
    csrfToken,
    timeoutMs: Math.max(2000, body.deadline_unix_ms - Date.now() + 2000),
  });
  return data as MutationResponse;
}
