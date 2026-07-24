import type { ReactElement } from "react";
import { render, type RenderOptions } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

/** Render a component inside a MemoryRouter for tests that use router hooks. */
export function renderWithRouter(ui: ReactElement, options?: RenderOptions & { route?: string }) {
  return render(<MemoryRouter initialEntries={[options?.route ?? "/"]}>{ui}</MemoryRouter>, options);
}
