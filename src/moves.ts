/**
 * Move-block planning.
 *
 * Input: the matched tokens in new-sequence order, described by `src[k]` =
 * old index of the k-th matched token. Every matched token is classified as
 * either `kept` (part of the in-order backbone) or `moved` (part of a move
 * block). Objectives, lexicographic:
 *
 *   1. maximize the number of kept tokens ("保留 token 数");
 *   2. minimize the number of move blocks;
 *   3. tie-break by source position: minimize the sum of source (old)
 *      indices of moved tokens, i.e. prefer moving tokens that originate
 *      earlier in the source.
 *
 * Two adjacent moved tokens k-1, k (in new order) merge into one block iff
 * src[k] > src[k-1] and every old index strictly between them is unmatched
 * (deleted). This keeps block source spans contiguous while tolerating small
 * edits (deletions/insertions) inside a moved block, and guarantees that
 * block source spans never overlap.
 */

export interface MovePlan {
  /** kept[k] = true when the k-th matched token belongs to the backbone. */
  kept: boolean[];
  keptCount: number;
  movedCount: number;
  blockCount: number;
  movedSourceSum: number;
  optimal: boolean;
  budgetExhausted: boolean;
  nodesExplored: number;
}

class BudgetExceeded extends Error {
  nodesExplored: number;
  constructor(nodesExplored: number) {
    super('search budget exhausted');
    this.nodesExplored = nodesExplored;
  }
}

/** Prefix sums over the matched-old flags, for O(1) "matched index in range" queries. */
function matchedPrefixSums(matchedOld: boolean[]): number[] {
  const prefix = new Array<number>(matchedOld.length + 1).fill(0);
  for (let i = 0; i < matchedOld.length; i++) {
    prefix[i + 1] = prefix[i] + (matchedOld[i] ? 1 : 0);
  }
  return prefix;
}

/**
 * mergeable[k] (k >= 1): may matched token k extend the block of token k-1?
 * Requires increasing source indices and no matched old index strictly
 * between them (any such token would belong to the backbone or another
 * block, which would make the source spans overlap).
 */
export function computeMergeable(src: number[], matchedOld: boolean[]): boolean[] {
  const prefix = matchedPrefixSums(matchedOld);
  const mergeable = new Array<boolean>(src.length).fill(false);
  for (let k = 1; k < src.length; k++) {
    const prev = src[k - 1];
    const curr = src[k];
    mergeable[k] = curr > prev && prefix[curr] - prefix[prev + 1] === 0;
  }
  return mergeable;
}

function countFromDecisions(
  src: number[],
  kept: boolean[],
  mergeable: boolean[],
): { blockCount: number; movedSourceSum: number } {
  let blockCount = 0;
  let movedSourceSum = 0;
  for (let k = 0; k < src.length; k++) {
    if (kept[k]) continue;
    movedSourceSum += src[k];
    if (k === 0 || kept[k - 1] || !mergeable[k]) blockCount++;
  }
  return { blockCount, movedSourceSum };
}

interface Node {
  kept: number;
  blocks: number;
  movedSum: number;
  parentKey: string | null;
  decision: boolean; // true = keep this token
}

function better(a: Node, b: Node): boolean {
  if (a.kept !== b.kept) return a.kept > b.kept;
  if (a.blocks !== b.blocks) return a.blocks < b.blocks;
  return a.movedSum < b.movedSum;
}

const keyOf = (lastKept: number, prevMoved: boolean): string =>
  `${lastKept}|${prevMoved ? 1 : 0}`;

/**
 * Exact dynamic program over matched tokens in new order.
 * State: (lastKept source index, whether the previous matched token moved).
 * Throws BudgetExceeded when `budget` state expansions are surpassed.
 */
function exactPlan(src: number[], mergeable: boolean[], budget: number): MovePlan {
  const kCount = src.length;
  let nodesExplored = 0;

  const start: Node = { kept: 0, blocks: 0, movedSum: 0, parentKey: null, decision: true };
  let current = new Map<string, Node>([[keyOf(-1, false), start]]);
  const layers: Array<Map<string, Node>> = [current];

  for (let k = 0; k < kCount; k++) {
    const o = src[k];
    const next = new Map<string, Node>();
    for (const [key, node] of current) {
      nodesExplored++;
      if (nodesExplored > budget) throw new BudgetExceeded(nodesExplored);
      const sep = key.indexOf('|');
      const lastKept = Number(key.slice(0, sep));
      const prevMoved = key.charCodeAt(sep + 1) === 49; // '1'

      // Option 1: keep token k (only if it extends the increasing backbone).
      if (o > lastKept) {
        const cand: Node = {
          kept: node.kept + 1,
          blocks: node.blocks,
          movedSum: node.movedSum,
          parentKey: key,
          decision: true,
        };
        const nk = keyOf(o, false);
        const existing = next.get(nk);
        if (!existing || better(cand, existing)) next.set(nk, cand);
      }

      // Option 2: move token k.
      const startsBlock = !prevMoved || !mergeable[k];
      const cand: Node = {
        kept: node.kept,
        blocks: node.blocks + (startsBlock ? 1 : 0),
        movedSum: node.movedSum + o,
        parentKey: key,
        decision: false,
      };
      const nk = keyOf(lastKept, true);
      const existing = next.get(nk);
      if (!existing || better(cand, existing)) next.set(nk, cand);
    }
    current = next;
    layers.push(current);
  }

  // Pick the best final state and walk the parent chain back.
  let bestKey = '';
  let bestNode: Node | null = null;
  for (const [key, node] of current) {
    if (bestNode === null || better(node, bestNode)) {
      bestNode = node;
      bestKey = key;
    }
  }

  const kept = new Array<boolean>(kCount).fill(false);
  let cursor: string | null = bestKey;
  for (let k = kCount; k >= 1; k--) {
    const node = layers[k].get(cursor as string);
    if (!node) throw new Error('internal error: broken parent chain');
    kept[k - 1] = node.decision;
    cursor = node.parentKey;
  }

  const { blockCount, movedSourceSum } = countFromDecisions(src, kept, mergeable);
  return {
    kept,
    keptCount: kept.filter(Boolean).length,
    movedCount: kCount - kept.filter(Boolean).length,
    blockCount,
    movedSourceSum,
    optimal: true,
    budgetExhausted: false,
    nodesExplored,
  };
}

/** Greedy fallback: backbone = one longest increasing subsequence, rest moves. */
function greedyPlan(src: number[], mergeable: boolean[], nodesExplored: number): MovePlan {
  const kCount = src.length;
  // Patience-based LIS; deterministic (leftmost insertion via lower bound).
  const tails: number[] = []; // tails[len] = index k of smallest tail src for that length
  const prev = new Array<number>(kCount).fill(-1);
  for (let k = 0; k < kCount; k++) {
    let lo = 0;
    let hi = tails.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (src[tails[mid]] < src[k]) lo = mid + 1;
      else hi = mid;
    }
    if (lo > 0) prev[k] = tails[lo - 1];
    if (lo === tails.length) tails.push(k);
    else tails[lo] = k;
  }
  const kept = new Array<boolean>(kCount).fill(false);
  let cursor = tails.length > 0 ? tails[tails.length - 1] : -1;
  while (cursor >= 0) {
    kept[cursor] = true;
    cursor = prev[cursor];
  }
  const { blockCount, movedSourceSum } = countFromDecisions(src, kept, mergeable);
  const keptCount = kept.filter(Boolean).length;
  return {
    kept,
    keptCount,
    movedCount: kCount - keptCount,
    blockCount,
    movedSourceSum,
    optimal: false,
    budgetExhausted: true,
    nodesExplored,
  };
}

/**
 * Plans kept/moved classification for the matched tokens.
 * `src[k]` is the old index of the k-th matched token (new order);
 * `matchedOld[o]` marks old indices that are matched (not deleted).
 */
export function planMoves(src: number[], matchedOld: boolean[], budget: number): MovePlan {
  const mergeable = computeMergeable(src, matchedOld);
  try {
    return exactPlan(src, mergeable, budget);
  } catch (err) {
    if (err instanceof BudgetExceeded) {
      return greedyPlan(src, mergeable, err.nodesExplored);
    }
    throw err;
  }
}
