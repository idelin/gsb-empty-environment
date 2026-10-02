import { solve, type SolveResult } from './solver.ts';
import type { SolveInput } from './model.ts';

/**
 * Re-weighting (权重更正): distribute `total` under NEW weights while
 * staying as close as possible to the PREVIOUS allocation.
 *
 * Lexicographic objective, solved globally over all item and overlapping
 * group constraints at once (never group-by-group, which could break
 * another group's constraints):
 *   1. minimize total movement  sum|x_i - prev_i|   (最少移动)
 *   2. minimize total deviation sum|x_i - ideal_i|  (再优化)
 *
 * If the budget cannot cover all lower bounds, the instance is reported
 * infeasible and the shortfall is kept PENDING (保留未决) — nothing is
 * forced into an invalid allocation.
 */
export function reallocate(
  input: Omit<SolveInput, 'previous'> & { previous: Record<string, bigint> },
): SolveResult {
  return solve(input);
}
