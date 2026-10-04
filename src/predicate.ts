import type { CompareOp, TriState, Value } from './types.ts';

/**
 * SQL three-valued comparison: any comparison touching null is 'unknown'.
 * A filter keeps a row only when the predicate evaluates to 'true'.
 */
export function compare3(a: Value, op: CompareOp, b: Value): TriState {
  if (a === null || b === null) return 'unknown';
  switch (op) {
    case '=':
      return a === b ? 'true' : 'false';
    case '!=':
      return a !== b ? 'true' : 'false';
    case '<':
      return a < b ? 'true' : 'false';
    case '<=':
      return a <= b ? 'true' : 'false';
    case '>':
      return a > b ? 'true' : 'false';
    case '>=':
      return a >= b ? 'true' : 'false';
  }
}

/**
 * Total order for the single-key sort. Nulls always sort last; the executor
 * adds the input sequence number as a tiebreaker, so equal keys keep their
 * original input order (stable), no matter when key values were materialized.
 */
export function makeKeyComparator(direction: 'asc' | 'desc'): (a: Value, b: Value) => number {
  const sign = direction === 'asc' ? 1 : -1;
  return (a: Value, b: Value): number => {
    if (a === null && b === null) return 0;
    if (a === null) return 1;
    if (b === null) return -1;
    if (a < b) return -1 * sign;
    if (a > b) return 1 * sign;
    return 0;
  };
}
