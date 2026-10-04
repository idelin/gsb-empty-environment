/**
 * Brute-force enumeration of optimal edit paths.
 *
 * Exponential in the input size; intended for cross-checking the dynamic
 * programming results on small sequences (ties created by zero-cost
 * matches and repeated symbols, in particular).
 *
 * Paths are emitted in the public ranking order ({@link MOVE_ORDER}):
 * lexicographic by operation sequence with `diagonal < delete < insert`.
 * Filtering the enumeration to minimum-cost paths preserves that order,
 * so `paths[k]` must equal `new EditDistance(source, target, costs).unrank(k)`.
 */

import {
  MOVE_ORDER,
  validateCosts,
  type Operation,
  type OperationCosts,
} from './editDistance.ts';

export interface EnumerationResult {
  readonly cost: number;
  readonly paths: Operation[][];
}

export function enumerateOptimalPaths(
  source: string,
  target: string,
  costs: OperationCosts,
  maxPaths = 1_000_000,
): EnumerationResult {
  validateCosts(costs);
  const src = Array.from(source);
  const tgt = Array.from(target);
  const m = src.length;
  const n = tgt.length;
  const optimal: Operation[][] = [];
  let best = Infinity;
  let visited = 0;

  const walk = (i: number, j: number, cost: number, ops: Operation[]): void => {
    if (cost > best) return;
    if (i === m && j === n) {
      visited++;
      if (visited > maxPaths) {
        throw new Error(`enumeration aborted: more than ${maxPaths} paths`);
      }
      if (cost < best) {
        best = cost;
        optimal.length = 0;
      }
      optimal.push([...ops]);
      return;
    }
    for (const move of MOVE_ORDER) {
      if (move === 'diagonal') {
        if (i >= m || j >= n) continue;
        const a = src[i];
        const b = tgt[j];
        if (a === b) {
          ops.push({ type: 'match', char: a });
          walk(i + 1, j + 1, cost, ops);
        } else {
          ops.push({ type: 'substitute', from: a, to: b });
          walk(i + 1, j + 1, cost + costs.substitute, ops);
        }
        ops.pop();
      } else if (move === 'delete') {
        if (i >= m) continue;
        ops.push({ type: 'delete', char: src[i] });
        walk(i + 1, j, cost + costs.delete, ops);
        ops.pop();
      } else {
        if (j >= n) continue;
        ops.push({ type: 'insert', char: tgt[j] });
        walk(i, j + 1, cost + costs.insert, ops);
        ops.pop();
      }
    }
  };

  walk(0, 0, 0, []);
  return { cost: best, paths: optimal };
}
