import type { DiffOptions, DiffResult, TreeNode, CostModel } from './types.ts';
import { DEFAULT_COSTS } from './types.ts';
import { indexTree, treeEquals } from './tree.ts';
import { forcedPairs, greedyMatch } from './match.ts';
import { generateScript } from './script.ts';
import { search } from './search.ts';
import { verifyPatch } from './apply.ts';

/**
 * Computes an edit script transforming `src` into `dst`.
 *
 * Pipeline:
 *  1. forced matching of stable ids (roots always matched)
 *  2. greedy seed matching -> guaranteed-complete incumbent patch
 *  3. anytime branch-and-bound over matchings, minimizing the weighted
 *     edit-script cost, until the space is exhausted (`optimal: true`) or
 *     the budget runs out (`optimal: false`; the incumbent patch is still
 *     complete and applicable)
 *
 * Every returned patch is checked against the target with the independent
 * applier before being handed back.
 */
export function diff(src: TreeNode, dst: TreeNode, options: DiffOptions = {}): DiffResult {
  const costs: CostModel = { ...DEFAULT_COSTS, ...(options.costs ?? {}) };
  const budget = options.budget ?? 4000;

  if (treeEquals(src, dst)) {
    return { patch: [], cost: 0, optimal: true, expanded: 0 };
  }

  const srcIdx = indexTree(src);
  const dstIdx = indexTree(dst);
  const forced = forcedPairs(srcIdx, dstIdx);

  const seed = greedyMatch(srcIdx, dstIdx, forced);
  const seedGen = generateScript(seed, srcIdx, dstIdx, forced, costs);
  let result = {
    ops: seedGen.ops,
    cost: seedGen.cost,
    matching: seedGen.matching,
    optimal: false,
    expanded: 0,
  };
  if (result.cost === 0) {
    return { patch: result.ops, cost: 0, optimal: true, expanded: 0 };
  }
  if (budget > 0) {
    result = search(srcIdx, dstIdx, forced, costs, budget, result);
  }

  // Internal consistency guard: the independent applier must be able to
  // replay the patch onto the target exactly.
  if (!verifyPatch(src, result.ops, dst)) {
    throw new Error('internal: generated patch failed independent verification');
  }
  return { patch: result.ops, cost: result.cost, optimal: result.optimal, expanded: result.expanded };
}
