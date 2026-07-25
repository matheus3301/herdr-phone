import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup, configure } from "@testing-library/react";

// findBy*/waitFor default to 1s, which a loaded machine running the whole suite
// in parallel jsdom workers can exceed for a Radix portal mount. Kept modest on
// purpose: a long patience hides a missing `await` on the thing under test
// instead of fixing it, which is how the delivery-state tests came to be flaky.
configure({ asyncUtilTimeout: 3000 });

afterEach(() => {
  cleanup();
});

// jsdom lacks matchMedia; provide a controllable stub defaulting to no-match.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as unknown as MediaQueryList;
}

// jsdom lacks ResizeObserver, used by the terminal fit logic.
if (!("ResizeObserver" in globalThis)) {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}

// Silence noisy scrollIntoView / not-implemented in jsdom where irrelevant.
if (!HTMLElement.prototype.scrollIntoView) {
  HTMLElement.prototype.scrollIntoView = vi.fn();
}
