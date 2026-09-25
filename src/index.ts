import type { JsonValue } from './json.ts';
import { planMoves } from './plan.ts';
import type { PlannedMove, Rule } from './plan.ts';
import { applyMoves } from './apply.ts';

export type { JsonValue } from './json.ts';
export type { Rule, MoveOp, PlannedMove } from './plan.ts';
export { planMoves } from './plan.ts';
export { applyMoves } from './apply.ts';
export { parsePath, formatPath, escapeSegment } from './path.ts';
export { PathSyntaxError, RuleError, MoveConflictError } from './errors.ts';

export interface MoveResult {
  tree: JsonValue;
  plan: PlannedMove[];
}

/**
 * Plan moves from `rules` against the original tree, verify conflicts,
 * then apply. The input tree is never mutated, and rule output is never
 * re-matched (no recursive rewriting of newly generated nodes).
 */
export function movePaths(tree: JsonValue, rules: Rule[]): MoveResult {
  const plan = planMoves(tree, rules);
  return { tree: applyMoves(tree, plan), plan };
}
