import type { Combo } from "./types.ts";
import { popcount } from "./core.ts";

export interface SolveResult {
  comboIndexes: number[];
  totalCost: number;
}

const DOMINANCE_PRUNE_LIMIT = 5000;

export function pruneDominated(candidates: Combo[]): Combo[] {
  if (candidates.length > DOMINANCE_PRUNE_LIMIT) return candidates;
  const keep = new Array<boolean>(candidates.length).fill(true);
  for (let i = 0; i < candidates.length; i++) {
    if (!keep[i]) continue;
    for (let j = 0; j < candidates.length; j++) {
      if (i === j || !keep[j]) continue;
      const a = candidates[j];
      const b = candidates[i];
      const coversSuperset = (b.pairMask & ~a.pairMask) === 0n;
      if (coversSuperset && (a.cost < b.cost || (a.cost === b.cost && a.pairMask !== b.pairMask))) {
        keep[i] = false;
        break;
      }
    }
  }
  return candidates.filter((_, i) => keep[i]);
}

function lowestBitIndex(mask: bigint): number {
  let bit = mask & -mask;
  let i = 0;
  while (bit > 1n) {
    bit >>= 1n;
    i++;
  }
  return i;
}

function* iterateBits(mask: bigint): Generator<number> {
  let m = mask;
  while (m !== 0n) {
    const i = lowestBitIndex(m);
    yield i;
    m &= m - 1n;
  }
}

export function solveGreedy(candidates: Combo[], reachableMask: bigint): SolveResult {
  let uncovered = reachableMask;
  const chosen: number[] = [];
  let totalCost = 0;
  while (uncovered !== 0n) {
    let bestIdx = -1;
    let bestNew = 0;
    for (let i = 0; i < candidates.length; i++) {
      const c = candidates[i];
      const newly = popcount(c.pairMask & uncovered);
      if (newly === 0) continue;
      if (bestIdx === -1) {
        bestIdx = i;
        bestNew = newly;
        continue;
      }
      const best = candidates[bestIdx];
      const lhs = newly * best.cost;
      const rhs = bestNew * c.cost;
      const betterRatio = lhs > rhs;
      const sameRatio = lhs === rhs;
      if (
        betterRatio ||
        (sameRatio && newly > bestNew) ||
        (sameRatio && newly === bestNew && c.cost < best.cost)
      ) {
        bestIdx = i;
        bestNew = newly;
      }
    }
    if (bestIdx === -1) break;
    chosen.push(bestIdx);
    totalCost += candidates[bestIdx].cost;
    uncovered &= ~candidates[bestIdx].pairMask;
  }
  return { comboIndexes: chosen, totalCost };
}

export function solveExact(candidates: Combo[], reachableMask: bigint): SolveResult {
  const usable = pruneDominated(
    candidates.filter((c) => (c.pairMask & reachableMask) !== 0n),
  );

  const pairMinCost = new Map<number, number>();
  const pairToCandidates = new Map<number, number[]>();
  for (const bit of iterateBits(reachableMask)) {
    pairMinCost.set(bit, Infinity);
    pairToCandidates.set(bit, []);
  }
  for (let i = 0; i < usable.length; i++) {
    for (const bit of iterateBits(usable[i].pairMask & reachableMask)) {
      pairToCandidates.get(bit)!.push(i);
      if (usable[i].cost < pairMinCost.get(bit)!) {
        pairMinCost.set(bit, usable[i].cost);
      }
    }
  }
  for (const list of pairToCandidates.values()) {
    list.sort((a, b) => usable[a].cost - usable[b].cost);
  }

  const greedy = solveGreedy(usable, reachableMask);
  let bestCost = greedy.totalCost;
  let bestSet = greedy.comboIndexes.slice();

  const memo = new Map<bigint, number>();
  const chosen: number[] = [];

  function lowerBound(uncovered: bigint): number {
    let lb = 0;
    for (const bit of iterateBits(uncovered)) {
      const c = pairMinCost.get(bit)!;
      if (c > lb) lb = c;
    }
    return lb;
  }

  function search(uncovered: bigint, cost: number): void {
    if (uncovered === 0n) {
      if (cost < bestCost) {
        bestCost = cost;
        bestSet = chosen.slice();
      }
      return;
    }
    if (cost + lowerBound(uncovered) >= bestCost) return;
    const seen = memo.get(uncovered);
    if (seen !== undefined && seen <= cost) return;
    memo.set(uncovered, cost);

    let pivot = -1;
    let pivotList: number[] = [];
    for (const bit of iterateBits(uncovered)) {
      const list = pairToCandidates.get(bit)!;
      if (pivot === -1 || list.length < pivotList.length) {
        pivot = bit;
        pivotList = list;
        if (list.length <= 1) break;
      }
    }
    for (const ci of pivotList) {
      const combo = usable[ci];
      if (cost + combo.cost >= bestCost) continue;
      chosen.push(ci);
      search(uncovered & ~combo.pairMask, cost + combo.cost);
      chosen.pop();
    }
  }

  search(reachableMask, 0);
  return { comboIndexes: bestSet, totalCost: bestCost };
}
