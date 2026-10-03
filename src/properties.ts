/**
 * Physical ordering properties.
 *
 * An `Ordering` is a list of columns (qualified as `alias.column`) with a
 * direction. A node "delivers" an ordering; a parent "requires" one.
 * The classic prefix rule decides satisfaction:
 * delivered [a, b, c] satisfies required [a, b] but not [b] or [a, c].
 */

export type Direction = 'asc' | 'desc';

export interface OrderColumn {
  column: string;
  direction: Direction;
}

export type Ordering = OrderColumn[];

export const ASC: Direction = 'asc';

export function asc(column: string): OrderColumn {
  return { column, direction: 'asc' };
}

export function toOrdering(columns: string[], direction: Direction = 'asc'): Ordering {
  return columns.map((column) => ({ column, direction }));
}

/** True when `delivered` satisfies `required` (prefix match on column + direction). */
export function satisfies(delivered: Ordering, required: Ordering): boolean {
  if (required.length === 0) return true;
  if (delivered.length < required.length) return false;
  return required.every(
    (req, i) =>
      delivered[i].column === req.column && delivered[i].direction === req.direction,
  );
}

export function formatOrdering(ordering: Ordering): string {
  if (ordering.length === 0) return '(unordered)';
  return ordering
    .map((o) => `${o.column}${o.direction === 'desc' ? ' DESC' : ''}`)
    .join(', ');
}
