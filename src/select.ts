import type { Block } from "./types.ts";

export interface Selection {
  /** Best non-overlapping block set found, sorted by oldStart. */
  blocks: Block[];
  covered: number;
  /** False when the budget ran out before optimality was proven. */
  optimal: boolean;
  statesExplored: number;
}

export interface SelectOptions {
  /** Search budget: max number of branch-and-bound states. */
  maxStates?: number;
  /** Sequence lengths, used to tighten the pruning bound. */
  oldLength?: number;
  newLength?: number;
}

/** Two blocks may coexist iff they overlap neither in old nor in new positions. */
export function blocksCompatible(a: Block, b: Block): boolean {
  const oldDisjoint =
    a.oldStart + a.length <= b.oldStart || b.oldStart + b.length <= a.oldStart;
  const newDisjoint =
    a.newStart + a.length <= b.newStart || b.newStart + b.length <= a.newStart;
  return oldDisjoint && newDisjoint;
}

export function coveredOf(blocks: Block[]): number {
  let total = 0;
  for (const b of blocks) total += b.length;
  return total;
}

/**
 * Lexicographic objective, best first:
 *  1. more preserved tokens wins;
 *  2. fewer blocks wins;
 *  3. source-position tie-break: the block list (sorted by oldStart) is
 *     compared element-wise by (oldStart, newStart, length), so solutions
 *     anchored earlier in the source win.
 * Returns a negative number when `a` is strictly better than `b`.
 */
export function compareBlockLists(a: Block[], b: Block[]): number {
  const coveredDiff = coveredOf(b) - coveredOf(a);
  if (coveredDiff !== 0) return coveredDiff;
  if (a.length !== b.length) return a.length - b.length;
  for (let i = 0; i < a.length; i++) {
    if (a[i].oldStart !== b[i].oldStart) return a[i].oldStart - b[i].oldStart;
    if (a[i].newStart !== b[i].newStart) return a[i].newStart - b[i].newStart;
    if (a[i].length !== b[i].length) return b[i].length - a[i].length;
  }
  return 0;
}

function byOldStart(a: Block, b: Block): number {
  return a.oldStart - b.oldStart || a.newStart - b.newStart || b.length - a.length;
}

/**
 * Branch-and-bound over candidate blocks with a state budget.
 * Always returns a valid (verifiable) selection; `optimal` reports whether
 * the search completed within `maxStates`.
 */
export function selectBlocks(candidates: Block[], options: SelectOptions = {}): Selection {
  const maxStates = options.maxStates ?? 200_000;
  const cands = [...candidates].sort(byOldStart);
  const k = cands.length;

  const suffixSum = new Array<number>(k + 1).fill(0);
  const suffixMax = new Array<number>(k + 1).fill(0);
  for (let i = k - 1; i >= 0; i--) {
    suffixSum[i] = suffixSum[i + 1] + cands[i].length;
    suffixMax[i] = Math.max(suffixMax[i + 1], cands[i].length);
  }
  const oldLength = options.oldLength ?? Number.POSITIVE_INFINITY;
  const newLength = options.newLength ?? Number.POSITIVE_INFINITY;

  // Greedy seed: longest compatible blocks first. Guarantees a decent
  // solution even when the budget is exhausted immediately.
  const byLength = [...cands].sort(
    (a, b) => b.length - a.length || a.oldStart - b.oldStart || a.newStart - b.newStart,
  );
  let best: Block[] = [];
  for (const c of byLength) {
    if (best.every((s) => blocksCompatible(s, c))) best.push(c);
  }
  best.sort(byOldStart);
  let bestCovered = coveredOf(best);

  const chosen: Block[] = [];
  const chosenNewIntervals: Block[] = [];
  let chosenCovered = 0;
  let maxOldEnd = 0;
  let states = 0;
  let exhausted = false;

  function dfs(index: number): void {
    if (exhausted) return;
    states++;
    if (states > maxStates) {
      exhausted = true;
      return;
    }
    // Any block taken from here on lies inside [maxOldEnd, oldLength) on the
    // old axis and needs chosenCovered-disjoint new positions.
    const bound =
      chosenCovered +
      Math.min(suffixSum[index], oldLength - maxOldEnd, newLength - chosenCovered);
    if (bound < bestCovered) return;
    if (bound === bestCovered) {
      const minAdditional =
        chosenCovered >= bestCovered
          ? 0
          : Math.ceil((bestCovered - chosenCovered) / Math.max(1, suffixMax[index]));
      if (chosen.length + minAdditional > best.length) return;
    }
    if (index === k) {
      if (compareBlockLists(chosen, best) < 0) {
        best = [...chosen];
        bestCovered = chosenCovered;
      }
      return;
    }
    const c = cands[index];
    // Take branch first so good solutions are found early.
    if (
      c.oldStart >= maxOldEnd &&
      chosenNewIntervals.every((b) => blocksCompatible(b, c))
    ) {
      const prevMaxOldEnd = maxOldEnd;
      chosen.push(c);
      chosenNewIntervals.push(c);
      chosenCovered += c.length;
      maxOldEnd = Math.max(maxOldEnd, c.oldStart + c.length);
      dfs(index + 1);
      maxOldEnd = prevMaxOldEnd;
      chosenCovered -= c.length;
      chosenNewIntervals.pop();
      chosen.pop();
      if (exhausted) return;
    }
    dfs(index + 1);
  }

  dfs(0);
  return { blocks: best, covered: bestCovered, optimal: !exhausted, statesExplored: states };
}
