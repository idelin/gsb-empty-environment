export type Direction = 'asc' | 'desc';

export interface OrderColumn {
  column: string;
  direction: Direction;
}

export type Ordering = OrderColumn[];

export function asc(column: string): OrderColumn {
  return { column, direction: 'asc' };
}

export function desc(column: string): OrderColumn {
  return { column, direction: 'desc' };
}

/**
 * A provided ordering satisfies a required ordering when the required
 * columns are a prefix of the provided columns with matching directions.
 * (Simplified: no functional-dependency / equivalence-class reasoning.)
 */
export function satisfies(provided: Ordering, required: Ordering): boolean {
  if (required.length > provided.length) return false;
  return required.every(
    (req, i) =>
      req.column === provided[i].column && req.direction === provided[i].direction,
  );
}

export function formatOrdering(ordering: Ordering): string {
  return ordering
    .map((c) => `${c.column} ${c.direction.toUpperCase()}`)
    .join(', ');
}
