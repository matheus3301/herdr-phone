import { useEffect, useRef } from "react";

const SUFFIX = "Herdr Phone";

/**
 * Set the document title and move focus to the route's heading on navigation.
 *
 * A single-page app changes the whole screen without a page load, so without
 * this a screen-reader user is left wherever the last activation happened and
 * the browser's title never changes. The heading is focused programmatically
 * (`tabIndex={-1}`), so it announces once and does not join the tab order.
 */
export function useRouteTitle(title: string): React.RefObject<HTMLHeadingElement | null> {
  const heading = useRef<HTMLHeadingElement | null>(null);
  const announced = useRef<string | null>(null);

  useEffect(() => {
    document.title = title ? `${title} · ${SUFFIX}` : SUFFIX;
  }, [title]);

  useEffect(() => {
    if (announced.current === title) return;
    announced.current = title;
    heading.current?.focus({ preventScroll: true });
  }, [title]);

  return heading;
}
