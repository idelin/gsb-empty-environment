import { CompareOp, Value } from './types';

/**
 * Three-valued logic: any null operand makes the result `null` (unknown).
 * Filters keep a row only when the predicate evaluates to `true`.
 */
export function applyOp(op: CompareOp, a: Value, b: Value): boolean | null {
  if (a === null || b === null) return null;
  switch (op) {
    case 'eq': return a === b;
    case 'neq': return a !== b;
    case 'lt': return (a as number) < (b as number);
    case 'lte': return (a as number) <= (b as number);
    case 'gt': return (a as number) > (b as number);
    case 'gte': return (a as number) >= (b as number);
  }
}

/**
 * Sort-key comparison. Nulls never compare equal to non-nulls (unknown),
 * so they are placed last; null vs null is a tie and falls through to the
 * arrival-sequence tiebreak, which keeps the sort stable.
 */
export function compareSortKeys(a: Value, b: Value): number {
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  if ((a as number) < (b as number)) return -1;
  if ((a as number) > (b as number)) return 1;
  return 0;
}
