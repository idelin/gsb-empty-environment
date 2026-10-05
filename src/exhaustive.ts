import type { CostModel, Op, TreeNode } from './types.ts';
import { DEFAULT_COSTS } from './types.ts';
import { indexTree } from './tree.ts';
import { pairable, UNMATCHED } from './match.ts';
import type { Matching } from './match.ts';
import { forcedPairs } from './match.ts';
import { generateScript } from './script.ts';
import { diff } from './diff.ts';
import { verifyPatch } from './apply.ts';

/**
 * Exhaustive checker for small trees.
 *
 * Enumerates *every* matching (respecting forced id pairs and pairability),
 * generates the induced edit script for each, and collects the true minimum
 * cost together with the set of distinct minimum-cost scripts. Used to
 * prove the optimizer's answers on small inputs and to check that its
 * tie-breaking between equivalent (equal-cost) solutions is deterministic.
 */

export interface ExhaustiveResult {
  minCost: number;
  /** Canonical (JSON) forms of all distinct minimum-cost scripts. */
  minPatches: string[];
  /** Number of complete matchings enumerated. */
  matchings: number;
  /** True when enumeration stopped at the safety cap. */
  truncated: boolean;
}

export function exhaustiveMinCost(
  src: TreeNode,
  dst: TreeNode,
  costs: CostModel = DEFAULT_COSTS,
  cap = 200000,
): ExhaustiveResult {
  const srcIdx = indexTree(src);
  const dstIdx = indexTree(dst);
  const forced = forcedPairs(srcIdx, dstIdx);
  const nt = dstIdx.nodes.length;
  const ns = srcIdx.nodes.length;
  const order = dstIdx.bfs;

  const candidates: number[][] = new Array(nt);
  for (let t = 0; t < nt; t++) {
    if (forced[t] >= 0) {
      candidates[t] = [forced[t]];
      continue;
    }
    const list: number[] = [];
    for (let s = 0; s < ns; s++) {
      if (pairable(srcIdx.nodes[s], dstIdx.nodes[t])) list.push(s);
    }
    candidates[t] = list;
  }

  let minCost = Number.POSITIVE_INFINITY;
  const minPatches = new Set<string>();
  let matchings = 0;
  let truncated = false;

  const pairs = new Array<number>(nt).fill(-1);
  const used = new Array<boolean>(ns).fill(false);

  const rec = (ti: number): void => {
    if (truncated) return;
    if (ti === order.length) {
      matchings++;
      if (matchings > cap) {
        truncated = true;
        return;
      }
      const matching: Matching = new Map();
      for (let t = 0; t < nt; t++) {
        if (pairs[t] >= 0) matching.set(pairs[t], t);
      }
      const gen = generateScript(matching, srcIdx, dstIdx, forced, costs);
      if (gen.cost < minCost) {
        minCost = gen.cost;
        minPatches.clear();
        minPatches.add(JSON.stringify(gen.ops));
      } else if (gen.cost === minCost) {
        minPatches.add(JSON.stringify(gen.ops));
      }
      return;
    }
    const t = order[ti];
    for (const s of candidates[t]) {
      if (used[s]) continue;
      pairs[t] = s;
      used[s] = true;
      rec(ti + 1);
      used[s] = false;
    }
    if (forced[t] < 0) {
      pairs[t] = UNMATCHED;
      rec(ti + 1);
    }
    pairs[t] = -1;
  };
  rec(0);

  return { minCost, minPatches: [...minPatches].sort(), matchings, truncated };
}

export interface DeterminismReport {
  ok: boolean;
  optimal: boolean;
  cost: number;
  minCost: number;
  /** Number of distinct minimum-cost scripts (equivalent solutions). */
  equivalentSolutions: number;
  problems: string[];
}

/**
 * Checks, on a (small) tree pair:
 *  - the differ's patch applies cleanly onto the target;
 *  - repeated runs produce byte-identical patches (deterministic choice
 *    among equivalent minimum-cost solutions);
 *  - when the differ claims optimality, its cost equals the exhaustive
 *    minimum and its patch is one of the enumerated minimum-cost scripts.
 */
export function checkDeterminism(
  src: TreeNode,
  dst: TreeNode,
  runs = 3,
  budget = 100000,
): DeterminismReport {
  const problems: string[] = [];
  const first = diff(src, dst, { budget });
  if (!verifyPatch(src, first.patch, dst)) problems.push('patch does not verify against target');
  const canonical = JSON.stringify(first.patch);
  for (let i = 1; i < runs; i++) {
    const again = diff(src, dst, { budget });
    if (JSON.stringify(again.patch) !== canonical) {
      problems.push(`run ${i + 1} produced a different patch`);
    }
  }
  const exh = exhaustiveMinCost(src, dst);
  if (!exh.truncated) {
    if (first.optimal && first.cost !== exh.minCost) {
      problems.push(`claimed optimal cost ${first.cost} but exhaustive minimum is ${exh.minCost}`);
    }
    if (first.cost === exh.minCost && !exh.minPatches.includes(canonical)) {
      problems.push('min-cost patch is not among the enumerated minimum-cost scripts');
    }
  }
  return {
    ok: problems.length === 0,
    optimal: first.optimal,
    cost: first.cost,
    minCost: exh.truncated ? -1 : exh.minCost,
    equivalentSolutions: exh.truncated ? -1 : exh.minPatches.length,
    problems,
  };
}
