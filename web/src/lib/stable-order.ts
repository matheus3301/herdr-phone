/**
 * Interaction-stable list ordering.
 *
 * The inbox reorders as agents change state, which is correct until the moment
 * a finger is on a row: a row that slides out from under a tap sends the
 * operator into the wrong run. While a list is being interacted with, existing
 * rows keep the position and the section they already had and genuinely new rows
 * are appended; once interaction ends the list settles into the live order.
 */

export interface Identified {
  id: string;
}

/**
 * Hold a *sectioned* list steady against a frozen reference. A row keeps both
 * its position and its section, while its content still refreshes in place from
 * the live data. Rows that disappeared are dropped and genuinely new rows join
 * the end of their live section.
 */
export function stabilizeGroups<R extends Identified, G extends { key: string; runs: R[] }>(
  frozen: readonly G[] | null,
  live: readonly G[],
): G[] {
  if (!frozen || frozen.length === 0) return [...live];

  const current = new Map<string, R>();
  for (const group of live) for (const row of group.runs) current.set(row.id, row);

  const placed = new Set<string>();
  const held = frozen.map((group) => {
    const runs: R[] = [];
    for (const row of group.runs) {
      const fresh = current.get(row.id);
      if (fresh && !placed.has(fresh.id)) {
        runs.push(fresh);
        placed.add(fresh.id);
      }
    }
    return { ...group, runs };
  });

  const byKey = new Map(held.map((group) => [group.key, group]));
  for (const group of live) {
    for (const row of group.runs) {
      if (placed.has(row.id)) continue;
      byKey.get(group.key)?.runs.push(row);
    }
  }
  return held;
}
