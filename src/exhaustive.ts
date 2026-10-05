import { blocksCompatible, compareBlockLists } from "./select.ts";
import type { Block } from "./types.ts";

/**
 * Exact optimum over the given candidate set by enumerating all subsets.
 * Only feasible for small candidate sets (<= 24). Used by tests to
 * cross-check the branch-and-bound search.
 */
export function exhaustiveSelect(candidates: Block[]): Block[] {
  const cands = [...candidates].sort(
    (a, b) => a.oldStart - b.oldStart || a.newStart - b.newStart || b.length - a.length,
  );
  const k = cands.length;
  if (k > 24) throw new Error(`too many candidates for exhaustive search: ${k}`);
  let best: Block[] = [];
  for (let mask = 0; mask < 1 << k; mask++) {
    const selected: Block[] = [];
    let ok = true;
    for (let i = 0; i < k && ok; i++) {
      if ((mask & (1 << i)) === 0) continue;
      const c = cands[i];
      for (const s of selected) {
        if (!blocksCompatible(s, c)) {
          ok = false;
          break;
        }
      }
      if (ok) selected.push(c);
    }
    if (ok && compareBlockLists(selected, best) < 0) best = selected;
  }
  return best;
}

interface MaskedBlock extends Block {
  oldMask: number;
  newMask: number;
}

/**
 * Best achievable coverage over ALL common blocks (not just maximal ones),
 * via bitmask dynamic programming. Only feasible for tiny inputs
 * (each sequence <= 10 tokens). Used by tests to confirm that restricting
 * candidates to maximal blocks does not lose coverage.
 */
export function bruteForceBestCoverage(oldTokens: string[], newTokens: string[]): number {
  const n = oldTokens.length;
  const m = newTokens.length;
  if (n > 10 || m > 10) throw new Error("input too large for bitmask brute force");
  const blocks: MaskedBlock[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < m; j++) {
      let maxLen = 0;
      while (i + maxLen < n && j + maxLen < m && oldTokens[i + maxLen] === newTokens[j + maxLen]) {
        maxLen++;
      }
      let oldMask = 0;
      let newMask = 0;
      for (let l = 1; l <= maxLen; l++) {
        oldMask |= 1 << (i + l - 1);
        newMask |= 1 << (j + l - 1);
        blocks.push({ oldStart: i, newStart: j, length: l, oldMask, newMask });
      }
    }
  }
  const memo = new Map<number, number>();
  const solve = (oldMask: number, newMask: number): number => {
    const key = (oldMask << 16) | newMask;
    const cached = memo.get(key);
    if (cached !== undefined) return cached;
    let best = 0;
    for (const b of blocks) {
      if ((b.oldMask & oldMask) === 0 && (b.newMask & newMask) === 0) {
        const value = b.length + solve(oldMask | b.oldMask, newMask | b.newMask);
        if (value > best) best = value;
      }
    }
    memo.set(key, best);
    return best;
  };
  return solve(0, 0);
}
