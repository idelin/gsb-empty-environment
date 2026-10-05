import { popcount, bitsOf } from "./solver.ts";

/**
 * Structural lower bound (Kleitman-Spencer type). If a subset S of features
 * has all its internal pair-combos reachable, then any cover of size k
 * restricts to a binary 2-covering family of |S| columns of length k: the
 * columns' supports are pairwise incomparable (combos 01/10), intersecting
 * (combo 11) and co-intersecting (combo 00). Hence k must satisfy
 * maxCoveringFamily(k) >= |S|. maxCoveringFamily(N) is computed exactly as a
 * maximum clique in the compatibility graph over the subsets of [N].
 */

const maxkCache = new Map<number, number>();

function compatible(a: number, b: number, full: number): boolean {
  if ((a & b) === 0) return false; // disjoint: combo 11 missing
  if ((a | b) === full) return false; // union is [N]: combo 00 missing
  if ((a & ~b) === 0 || (b & ~a) === 0) return false; // comparable: 01/10 missing
  return true;
}

function maxCliqueSize(adj: bigint[], n: number): number {
  let best = 0;
  const expand = (cand: number[], path: number): void => {
    if (cand.length === 0) {
      if (path > best) best = path;
      return;
    }
    // Greedy coloring of the candidate set for an upper bound.
    const color = new Array<number>(cand.length);
    for (let ii = 0; ii < cand.length; ii++) {
      const v = cand[ii];
      let c = 1;
      for (;;) {
        let ok = true;
        for (let jj = 0; jj < ii; jj++) {
          if (color[jj] === c && (adj[cand[jj]] & (1n << BigInt(v))) !== 0n) {
            ok = false;
            break;
          }
        }
        if (ok) break;
        c++;
      }
      color[ii] = c;
    }
    const idx = cand.map((v, i) => i).sort((a, b) => color[a] - color[b]);
    for (let t = idx.length - 1; t >= 0; t--) {
      const v = cand[idx[t]];
      const c = color[idx[t]];
      if (path + c <= best) return;
      const newCand: number[] = [];
      for (let u = t - 1; u >= 0; u--) {
        const w = cand[idx[u]];
        if ((adj[v] & (1n << BigInt(w))) !== 0n) newCand.push(w);
      }
      expand(newCand, path + 1);
    }
  };
  expand(Array.from({ length: n }, (_, i) => i), 0);
  return best;
}

/** Exact maximum size of a binary 2-covering family with `rows` rows. */
export function maxCoveringFamily(rows: number): number {
  const cached = maxkCache.get(rows);
  if (cached !== undefined) return cached;
  const full = (1 << rows) - 1;
  const verts: number[] = [];
  for (let a = 1; a <= full; a++) verts.push(a);
  const m = verts.length;
  const adj: bigint[] = Array.from({ length: m }, () => 0n);
  for (let i = 0; i < m; i++) {
    for (let j = i + 1; j < m; j++) {
      if (compatible(verts[i], verts[j], full)) {
        adj[i] |= 1n << BigInt(j);
        adj[j] |= 1n << BigInt(i);
      }
    }
  }
  const size = maxCliqueSize(adj, m);
  maxkCache.set(rows, size);
  return size;
}

export interface StructuralBound {
  bound: number;
  subset: number[];
  /** maxCoveringFamily(bound - 1), proving bound - 1 rows are insufficient. */
  maxkBelow: number;
}

/**
 * Largest feature subset whose internal pair-combos are all reachable, turned
 * into a lower bound on the cover size.
 */
export function structuralLowerBound(n: number, reachable: bigint): StructuralBound {
  // pairMask[i][j]: 4-bit mask of reachable combos for feature pair (i, j).
  const pairReach: number[][] = Array.from({ length: n }, () => new Array(n).fill(0));
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const bits = Number((reachable >> BigInt((i * n + j) * 4)) & 0xfn);
      pairReach[i][j] = bits;
      pairReach[j][i] = bits;
    }
  }
  const full = (1 << n) - 1;
  let bestSubset = 0;
  let bestSize = 0;
  // Enumerate subsets in decreasing popcount; first fully-reachable one wins.
  const subsets: number[] = [];
  for (let s = 1; s <= full; s++) subsets.push(s);
  subsets.sort((a, b) => popcount(BigInt(b)) - popcount(BigInt(a)));
  for (const s of subsets) {
    const size = popcount(BigInt(s));
    if (size <= bestSize) break;
    let ok = true;
    outer: for (let i = 0; i < n && ok; i++) {
      if (((s >> i) & 1) === 0) continue;
      for (let j = i + 1; j < n; j++) {
        if (((s >> j) & 1) === 1 && pairReach[i][j] !== 0xf) {
          ok = false;
          continue outer;
        }
      }
    }
    if (ok) {
      bestSubset = s;
      bestSize = size;
    }
  }
  if (bestSize < 2) return { bound: 0, subset: [], maxkBelow: 0 };
  let bound = 1;
  while (maxCoveringFamily(bound) < bestSize) bound++;
  const subset: number[] = [];
  for (let i = 0; i < n; i++) if (((bestSubset >> i) & 1) === 1) subset.push(i);
  return { bound, subset, maxkBelow: maxCoveringFamily(bound - 1) };
}

export interface LpBound {
  /** Certified lower bound: ceil(dualValue), computed with exact integer loads. */
  bound: number;
  /** Dual packing value achieved (<= LP optimum <= IP optimum). */
  value: number;
  /** Per element-id dual weights (non-negative integers, unnormalized). */
  weights: Float64Array;
  /** Normalizer: every set's weight sum is <= maxLoad. */
  maxLoad: number;
}

/**
 * Certified LP lower bound for the set-cover instance via a feasible solution
 * to the dual packing LP (max sum y_e subject to every set containing weight
 * at most 1). Multiplicative-weights iterations produce integer element
 * counts y and integer set loads; y/maxLoad is exactly dual-feasible, so
 * ceil(sum(y)/maxLoad) is a rigorous bound. The per-element weights can be
 * reused inside branch-and-bound nodes: the weights restricted to the
 * uncovered elements stay dual-feasible for the subproblem.
 */
export function lpDualBound(
  setMasks: bigint[],
  reachable: bigint,
  target: number,
  maxIters = 400_000,
): LpBound {
  const elems = [...bitsOf(reachable)];
  const E = elems.length;
  const maxElemId = elems[E - 1];
  const posOf = new Map<number, number>(elems.map((e, i) => [e, i]));
  const S = setMasks.length;
  const setElems: number[][] = setMasks.map((m) => [...bitsOf(m)].map((e) => posOf.get(e)!));
  const elemSets: number[][] = Array.from({ length: E }, () => []);
  for (let s = 0; s < S; s++) {
    for (const e of setElems[s]) elemSets[e].push(s);
  }
  const eps = 0.1;
  const price = new Float64Array(S).fill(1);
  const cost = new Float64Array(E);
  for (let e = 0; e < E; e++) cost[e] = elemSets[e].length;
  const y = new Float64Array(E).fill(0);
  const load = new Float64Array(S).fill(0);
  let t = 0;
  let bound = 0;
  let value = 0;
  let plateau = 0;
  const chunk = 4096;
  outer: while (t < maxIters) {
    for (let u = 0; u < chunk && t < maxIters; u++, t++) {
      let be = 0;
      for (let e = 1; e < E; e++) if (cost[e] < cost[be]) be = e;
      y[be] += 1;
      for (const s of elemSets[be]) {
        load[s] += 1;
        const delta = price[s] * eps;
        price[s] += delta;
        for (const e2 of setElems[s]) cost[e2] += delta;
      }
      if ((t & 1023) === 0) {
        let mx = 0;
        for (let s = 0; s < S; s++) if (price[s] > mx) mx = price[s];
        if (mx > 1e60) {
          for (let s = 0; s < S; s++) price[s] /= 1e60;
          for (let e = 0; e < E; e++) cost[e] /= 1e60;
        }
      }
    }
    let maxLoad = 0;
    for (let s = 0; s < S; s++) if (load[s] > maxLoad) maxLoad = load[s];
    value = t / maxLoad;
    const cur = Math.ceil(value - 1e-9);
    if (cur > bound) {
      bound = cur;
      plateau = 0;
    } else {
      plateau++;
    }
    if (bound >= target || plateau >= 10) break outer;
  }
  let maxLoad = 0;
  for (let s = 0; s < S; s++) if (load[s] > maxLoad) maxLoad = load[s];
  const weights = new Float64Array(maxElemId + 1);
  for (let i = 0; i < E; i++) weights[elems[i]] = y[i];
  return { bound, value, weights, maxLoad };
}
