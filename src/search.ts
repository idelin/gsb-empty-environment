import type { Formula } from "./formula.ts";
import {
  MASK_A,
  MASK_B,
  MASK_N,
  MASK_ALL,
  SAT,
  VIOL,
  evalStatus,
  appendCounts,
} from "./formula.ts";

export interface SearchStats {
  nodesVisited: number;
  pruned: number;
  memoHits: number;
  memoStates: number;
  maxDepth: number;
  elapsedMs: number;
}

export interface ExistenceResult {
  found: boolean;
  stats: SearchStats;
}

export function existsDisjointPair(
  fa: Formula,
  fb: Formula,
  n: number,
  masks?: number[],
): ExistenceResult {
  const m0 = masks ? masks.slice() : new Array<number>(n).fill(MASK_ALL);
  const stats: SearchStats = {
    nodesVisited: 0,
    pruned: 0,
    memoHits: 0,
    memoStates: 0,
    maxDepth: 0,
    elapsedMs: 0,
  };
  const memo = new Set<string>();
  const countsA: number[] = [];
  const countsB: number[] = [];
  const t0 = performance.now();

  const key = (): string => {
    countsA.length = 0;
    countsB.length = 0;
    appendCounts(fa, MASK_A, m0, countsA);
    appendCounts(fb, MASK_B, m0, countsB);
    return countsA.join(",") + "|" + countsB.join(",");
  };

  const dfs = (depth: number): boolean => {
    const sa = evalStatus(fa, MASK_A, m0);
    const sb = evalStatus(fb, MASK_B, m0);
    if (sa === VIOL || sb === VIOL) {
      stats.pruned++;
      return false;
    }
    if (sa === SAT && sb === SAT) return true;
    let idx = -1;
    for (let i = 0; i < n; i++) {
      const mi = m0[i];
      if (mi !== MASK_A && mi !== MASK_B && mi !== MASK_N) {
        idx = i;
        break;
      }
    }
    if (idx === -1) return false;
    stats.nodesVisited++;
    if (depth > stats.maxDepth) stats.maxDepth = depth;
    const k = key();
    if (memo.has(k)) {
      stats.memoHits++;
      return false;
    }
    const orig = m0[idx];
    let options = orig;
    if (sa === SAT && (options & (options - 1)) !== 0) options &= ~MASK_A;
    if (sb === SAT && (options & (options - 1)) !== 0) options &= ~MASK_B;
    for (const bit of [MASK_A, MASK_B, MASK_N]) {
      if ((options & bit) === 0) continue;
      m0[idx] = bit;
      if (dfs(depth + 1)) return true;
    }
    m0[idx] = orig;
    memo.add(k);
    return false;
  };

  const found = dfs(0);
  stats.memoStates = memo.size;
  stats.elapsedMs = performance.now() - t0;
  return { found, stats };
}
