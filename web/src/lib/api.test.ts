import { describe, it, expect, vi, afterEach } from "vitest";
import * as api from "./api";

function mockFetch(impl: (url: string, init: RequestInit) => Response | Promise<Response>) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(impl(String(input), init ?? {})));
}

afterEach(() => vi.restoreAllMocks());

describe("api client decoding", () => {
  it("returns snapshot + etag on 200", async () => {
    global.fetch = mockFetch((_url) =>
      new Response(JSON.stringify({ hash: "h1", version: 1 }), { status: 200, headers: { ETag: '"h1"' } }),
    ) as unknown as typeof fetch;
    const res = await api.getSnapshot(null);
    expect(res.notModified).toBe(false);
    expect(res.snapshot?.hash).toBe("h1");
    expect(res.etag).toBe('"h1"');
  });

  it("handles 304 Not Modified without a body", async () => {
    global.fetch = mockFetch(() => new Response(null, { status: 304, headers: { ETag: '"h1"' } })) as unknown as typeof fetch;
    const res = await api.getSnapshot('"h1"');
    expect(res.notModified).toBe(true);
    expect(res.snapshot).toBeUndefined();
  });

  it("decodes a structured error into ApiError", async () => {
    global.fetch = mockFetch(() =>
      new Response(JSON.stringify({ error: { code: "pane_not_found", message: "gone", retryable: false } }), { status: 404 }),
    ) as unknown as typeof fetch;
    await expect(api.readPane("w1:p1", "visible", 100)).rejects.toMatchObject({
      code: "pane_not_found",
      status: 404,
    });
  });

  it("sends CSRF + content-type on mutations", async () => {
    const fetchMock = mockFetch((_url, init) => {
      const headers = new Headers(init.headers);
      expect(headers.get("X-CSRF-Token")).toBe("tok");
      expect(headers.get("Content-Type")).toBe("application/json");
      expect(init.method).toBe("POST");
      return new Response(JSON.stringify({ request_id: "r1", accepted: true, result: {} }), { status: 200 });
    });
    global.fetch = fetchMock as unknown as typeof fetch;
    const res = await api.mutate(
      { request_id: "r1", operation: "pane.split", deadline_unix_ms: Date.now() + 5000, params: {} },
      "tok",
    );
    expect("accepted" in res && res.accepted).toBe(true);
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("maps a network failure to a retryable ApiError", async () => {
    global.fetch = vi.fn(() => Promise.reject(new TypeError("boom"))) as unknown as typeof fetch;
    await expect(api.getCapabilities()).rejects.toMatchObject({ code: "network", retryable: true });
  });
});
