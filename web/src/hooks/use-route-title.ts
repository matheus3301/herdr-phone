import { useEffect, useLayoutEffect, useRef } from "react";
import { useLocation } from "react-router-dom";

const SUFFIX = "Herdr Phone";

/** Is focus currently inside a field the operator could be typing into? */
function editing(node: Element | null): boolean {
  if (!node) return false;
  if (node instanceof HTMLTextAreaElement) return true;
  if (node instanceof HTMLInputElement) return true;
  if (node instanceof HTMLElement && node.isContentEditable) return true;
  return false;
}

/**
 * Set the document title and move focus to the route's heading on navigation.
 *
 * A single-page app changes the whole screen without a page load, so without
 * this a screen-reader user is left wherever the last activation happened and
 * the browser's title never changes. The heading is focused programmatically
 * (`tabIndex={-1}`), so it announces once and does not join the tab order.
 *
 * Two things make the focus move safe rather than a hazard:
 *
 *  - It is keyed on **navigation identity**, not the title string. A live title
 *    is not a navigation: `RunHeader` passes `run.agentName`, which is resolved
 *    through a fallback chain, so an agent rename or a list refresh that changes
 *    which fallback wins would otherwise yank focus to the `<h1>` mid-typing and
 *    dismiss the software keyboard.
 *  - It runs in a **layout** effect and refuses to move focus out of an editable
 *    field. A passive effect can flush well after first paint — long after the
 *    route's own fields are focusable — and then it steals focus from whatever
 *    the operator already started typing in. On WebKit that loses the text
 *    outright, because an insertion targets whatever is focused at the moment it
 *    lands. Running before paint closes the window; the guard covers a late or
 *    re-entrant run.
 */
export function useRouteTitle(title: string): React.RefObject<HTMLHeadingElement | null> {
  const heading = useRef<HTMLHeadingElement | null>(null);
  const location = useLocation();
  // Path + query, deliberately excluding the hash: an in-page fragment link is
  // not a route change. The skip link is exactly that, and stealing focus back
  // to the heading is the one thing it must not do.
  const navigation = `${location.pathname}${location.search}`;
  const announced = useRef<string | null>(null);

  useEffect(() => {
    document.title = title ? `${title} · ${SUFFIX}` : SUFFIX;
  }, [title]);

  useLayoutEffect(() => {
    if (announced.current === navigation) return;
    announced.current = navigation;
    if (editing(document.activeElement)) return;
    heading.current?.focus({ preventScroll: true });
  }, [navigation]);

  return heading;
}
