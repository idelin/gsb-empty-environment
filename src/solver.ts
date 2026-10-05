import type { Constraint } from "./types.ts";
import { structuralLowerBound, maxCoveringFamily, lpDualBound, type StructuralBound, type LpBound } from "./bounds.ts";

export const MAX_FEATURES = 12;

export function isValidConfig(config: number, constraints: Constraint[]): boolean {
  for (const c of constraints) {
    if (c.type === "mutex") {
      if (((config >> c.a) & 1) === 1 && ((config >> c.b) & 1) === 1) return false;
    } else {
      if (((config >> c.from) & 1) === 1 && ((config >> c.to) & 1) === 0) return false;
    }
  }
  return true;
}

export function enumerateValidConfigs(n: number, constraints: Constraint[]): number[] {
  const valid: number[] = [];
  for (let c = 0; c < 1 << n; c++) {
    if (isValidConfig(c, constraints)) valid.push(c);
  }
  return valid;
}

export function popcount(x: bigint): number {
  let c = 0;
  while (x !== 0n) {
    x &= x - 1n;
    c++;
  }
  return c;
}

export function* bitsOf(x: bigint): Generator<number> {
  let i = 0;
  let v = x;
  while (v !== 0n) {
    if ((v & 1n) !== 0n) yield i;
    v >>= 1n;
    i++;
  }
}

/** Bit index of the pair-combo (feature i = vi, feature j = vj), i < j. */
export function comboIndex(n: number, i: number, j: number, vi: number, vj: number): number {
  return (i * n + j) * 4 + vi * 2 + vj;
}

export function comboBit(n: number, i: number, j: number, vi: number, vj: number): bigint {
  return 1n << BigInt(comboIndex(n, i, j, vi, vj));
}

/** Bitmask of all pair-combos a config exhibits: exactly C(n,2) bits. */
export function configMask(config: number, n: number): bigint {
  let m = 0n;
  for (let i = 0; i < n; i++) {
    const vi = (config >> i) & 1;
    for (let j = i + 1; j < n; j++) {
      const vj = (config >> j) & 1;
      m |= comboBit(n, i, j, vi, vj);
    }
  }
  return m;
}

/**
 * Keep only configs whose pair-mask is not a subset of another config's mask.
 * A dominated config can never be strictly better, so removing it preserves
 * the minimum cover size. Duplicates collapse to one representative.
 */
function reduceMasks(masks: bigint[]): number[] {
  const pops = masks.map(popcount);
  const order = masks.map((_, i) => i).sort((a, b) => pops[b] - pops[a] || a - b);
  const kept: number[] = [];
  const keptMasks: bigint[] = [];
  for (const i of order) {
    let dominated = false;
    for (const km of keptMasks) {
      if ((km & masks[i]) === masks[i]) {
        dominated = true;
        break;
      }
    }
    if (!dominated) {
      kept.push(i);
      keptMasks.push(masks[i]);
    }
  }
  return kept;
}

/**
 * Feature f is flippable iff negating it in every assignment preserves
 * validity. Flips form a symmetry group, so some optimal cover contains a
 * config whose flippable bits are all 0 (flip them away in one of its
 * configs). Used to restrict the first chosen config.
 */
function computeFlippable(n: number, constraints: Constraint[]): boolean[] {
  const flippable: boolean[] = [];
  for (let f = 0; f < n; f++) {
    let ok = true;
    for (let c = 0; c < 1 << n; c++) {
      if (isValidConfig(c, constraints) !== isValidConfig(c ^ (1 << f), constraints)) {
        ok = false;
        break;
      }
    }
    flippable.push(ok);
  }
  return flippable;
}

/**
 * Connected components of the "swap is an automorphism" relation. Swapping
 * two features in the same component preserves validity, and transpositions
 * generate the full symmetric group on each component, so features inside a
 * component are freely permutable.
 */
function computeComponents(n: number, constraints: Constraint[]): number[] {
  const parent = Array.from({ length: n }, (_, i) => i);
  const find = (x: number): number => {
    while (parent[x] !== x) {
      parent[x] = parent[parent[x]];
      x = parent[x];
    }
    return x;
  };
  const swapConfig = (c: number, f: number, g: number): number => {
    const bf = (c >> f) & 1;
    const bg = (c >> g) & 1;
    if (bf === bg) return c;
    return c ^ (1 << f) ^ (1 << g);
  };
  for (let f = 0; f < n; f++) {
    for (let g = f + 1; g < n; g++) {
      if (find(f) === find(g)) continue;
      let ok = true;
      for (let c = 0; c < 1 << n; c++) {
        if (isValidConfig(c, constraints) !== isValidConfig(swapConfig(c, f, g), constraints)) {
          ok = false;
          break;
        }
      }
      if (ok) parent[find(f)] = find(g);
    }
  }
  const ids = new Map<number, number>();
  return Array.from({ length: n }, (_, i) => {
    const r = find(i);
    if (!ids.has(r)) ids.set(r, ids.size);
    return ids.get(r)!;
  });
}

export interface SearchStat {
  target: number;
  nodes: number;
  prunedByBound: number;
  memoHits: number;
  failedStates: number;
  solved: boolean;
}

export interface SolveResult {
  n: number;
  validConfigs: number[];
  reachable: bigint;
  reachableCount: number;
  minCount: number;
  configs: number[];
  lowerBound: number;
  structural: StructuralBound;
  lp: { bound: number; value: number } | null;
  boundDetails: {
    pairBound: number;
    pairBoundPair: [number, number] | null;
    totalBound: number;
  };
  stats: SearchStat[];
  /** Self-check: chosen configs are valid and jointly cover every reachable pair. */
  verified: boolean;
}

const MEMO_CAP = 4_000_000;

export function solve(n: number, constraints: Constraint[]): SolveResult {
  const validConfigs = enumerateValidConfigs(n, constraints);
  const masks = validConfigs.map((c) => configMask(c, n));
  let reachable = 0n;
  for (const m of masks) reachable |= m;
  const reachableCount = popcount(reachable);

  const pairMasks: bigint[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      pairMasks.push(0xfn << BigInt((i * n + j) * 4));
    }
  }
  const fullSetSize = (n * (n - 1)) / 2;

  if (reachable === 0n) {
    return {
      n, validConfigs, reachable, reachableCount,
      minCount: 0, configs: [], lowerBound: 0,
      structural: { bound: 0, subset: [], maxkBelow: 0 },
      lp: null,
      boundDetails: { pairBound: 0, pairBoundPair: null, totalBound: 0 },
      stats: [], verified: true,
    };
  }

  const keepIdx = reduceMasks(masks);
  const sets = keepIdx.map((i) => masks[i]);
  const setConfigs = keepIdx.map((i) => validConfigs[i]);

  const elemSets = new Map<number, number[]>();
  for (let s = 0; s < sets.length; s++) {
    for (const e of bitsOf(sets[s])) {
      let arr = elemSets.get(e);
      if (!arr) elemSets.set(e, (arr = []));
      arr.push(s);
    }
  }
  const staticCount = (e: number): number => elemSets.get(e)!.length;

  // Cheap bound: per-feature-pair uncovered combos (a config covers at most
  // one combo per feature pair) and total/fullSetSize. Refined on demand with
  // the exact max number of uncovered combos any single set can still add.
  const lowerBoundOf = (uncovered: bigint, budget: number): number => {
    let total = 0;
    let maxPair = 0;
    for (const pm of pairMasks) {
      const c = popcount(uncovered & pm);
      total += c;
      if (c > maxPair) maxPair = c;
    }
    if (total === 0) return 0;
    const cheap = Math.max(maxPair, Math.ceil(total / fullSetSize));
    if (cheap > budget) return cheap;
    let maxAdd = 0;
    for (const m of sets) {
      const c = popcount(m & uncovered);
      if (c > maxAdd) maxAdd = c;
    }
    return Math.max(maxPair, Math.ceil(total / maxAdd));
  };

  // maxk table for the per-node clique bound: a set of still-fully-uncovered
  // feature pairs forms a clique whose columns must stay 2-covering, so if
  // the clique exceeds maxCoveringFamily(budget) the node is infeasible. Only
  // budgets <= 6 can prune (maxk(7) = 15 >= any clique for n <= 12).
  const maxkTable = [0, 1, 1, 1, 3, 4, 10, 15];
  for (let b = 1; b <= 6; b++) {
    // Sanity-check the hardcoded table against the exact computation.
    if (maxCoveringFamily(b) !== maxkTable[b]) throw new Error("maxk table mismatch");
  }
  const pairMaskAt = (i: number, j: number): bigint => 0xfn << BigInt((i * n + j) * 4);
  const cliquePrune = (uncovered: bigint, budget: number): boolean => {
    if (budget > 6) return false;
    const full: boolean[][] = Array.from({ length: n }, () => new Array(n).fill(false));
    const deg: number[] = new Array(n).fill(0);
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        if (popcount(uncovered & pairMaskAt(i, j)) === 4) {
          full[i][j] = full[j][i] = true;
          deg[i]++;
          deg[j]++;
        }
      }
    }
    const order = Array.from({ length: n }, (_, i) => i).sort((a, b) => deg[b] - deg[a]);
    const clique: number[] = [];
    for (const f of order) {
      if (clique.every((g) => full[f][g])) clique.push(f);
    }
    return clique.length > maxkTable[budget];
  };

  const flippable = computeFlippable(n, constraints);
  const flippableMask = flippable.reduce((acc, f, i) => (f ? acc | (1 << i) : acc), 0);
  const component = computeComponents(n, constraints);

  // Cell partition refining `component` by the bit patterns of the configs
  // chosen so far. Any permutation of features inside a cell is an
  // automorphism that fixes every chosen config, so the next config may be
  // assumed sorted (1s before 0s) inside each cell.
  const cellsOf = (
    prefix: number[],
  ): { members: number[][]; cellOf: number[]; sig: string } => {
    const keyOf = (f: number): string => {
      let bits = "";
      for (const c of prefix) bits += (c >> f) & 1;
      return component[f] + ":" + bits;
    };
    const ids = new Map<string, number>();
    const members: number[][] = [];
    const cellOf: number[] = new Array(n);
    for (let f = 0; f < n; f++) {
      const key = keyOf(f);
      let id = ids.get(key);
      if (id === undefined) {
        id = members.length;
        ids.set(key, id);
        members.push([]);
      }
      members[id].push(f);
      cellOf[f] = id;
    }
    return { members, cellOf, sig: cellOf.join(",") };
  };

  const isCanonical = (config: number, members: number[][]): boolean => {
    for (const cell of members) {
      let seenZero = false;
      for (const f of cell) {
        if (((config >> f) & 1) === 0) seenZero = true;
        else if (seenZero) return false;
      }
    }
    return true;
  };

  const structural = structuralLowerBound(n, reachable);
  let lowerBound = Math.max(lowerBoundOf(reachable, Infinity), structural.bound);
  let pairBound = 0;
  let pairBoundPair: [number, number] | null = null;
  {
    let idx = 0;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const c = popcount(reachable & pairMasks[idx]);
        if (c > pairBound) {
          pairBound = c;
          pairBoundPair = [i, j];
        }
        idx++;
      }
    }
  }
  const totalBound = Math.ceil(reachableCount / fullSetSize);
  const boundDetails = { pairBound, pairBoundPair, totalBound };

  // --- Constructive phase: greedy + stochastic local search for a witness ---
  const greedy = (): number[] => {
    let uncovered = reachable;
    const chosen: number[] = [];
    while (uncovered !== 0n) {
      let best = -1;
      let bestAdd = 0;
      for (let s = 0; s < sets.length; s++) {
        const add = popcount(sets[s] & uncovered);
        if (add > bestAdd) {
          bestAdd = add;
          best = s;
        }
      }
      chosen.push(best);
      uncovered &= ~sets[best];
    }
    return chosen;
  };

  const removeRedundant = (cover: number[]): number[] => {
    const result = [...cover];
    let changed = true;
    while (changed) {
      changed = false;
      for (let i = 0; i < result.length; i++) {
        let union = 0n;
        for (let j = 0; j < result.length; j++) {
          if (j !== i) union |= sets[result[j]];
        }
        if ((union & reachable) === reachable) {
          result.splice(i, 1);
          changed = true;
          break;
        }
      }
    }
    return result;
  };

  // Deterministic RNG (mulberry32) seeded from the input for reproducibility.
  let seed = (n * 2654435761) >>> 0;
  for (const c of constraints) {
    const t = c.type === "mutex" ? [1, c.a, c.b] : [2, c.from, c.to];
    for (const v of t) seed = ((seed ^ (v + 31)) * 2246822519) >>> 0;
  }
  const rng = (): number => {
    seed = (seed + 0x6d2b79f5) >>> 0;
    let t = seed;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  const localSearchCover = (k: number, maxIters: number): number[] | null => {
    const m = sets.length;
    if (k > m) return null;
    let sel: number[] = [];
    const restart = (): void => {
      const used = new Set<number>();
      sel = [];
      while (sel.length < k) {
        const s = Math.floor(rng() * m);
        if (!used.has(s)) {
          used.add(s);
          sel.push(s);
        }
      }
    };
    restart();
    let stall = 0;
    let restarts = 0;
    for (let iter = 0; iter < maxIters; iter++) {
      const prefixOr: bigint[] = new Array(sel.length + 1).fill(0n);
      for (let i = 0; i < sel.length; i++) prefixOr[i + 1] = prefixOr[i] | sets[sel[i]];
      const suffixOr: bigint[] = new Array(sel.length + 1).fill(0n);
      for (let i = sel.length - 1; i >= 0; i--) suffixOr[i] = suffixOr[i + 1] | sets[sel[i]];
      const union = prefixOr[sel.length];
      const unc = popcount(reachable & ~union);
      if (unc === 0) return sel;
      if (stall > 500) {
        if (++restarts > 12) return null;
        restart();
        stall = 0;
        continue;
      }
      stall++;
      let bestGain = 0;
      let bestOut = -1;
      let bestIn = -1;
      const samples = 64;
      for (let oi = 0; oi < sel.length; oi++) {
        const wo = prefixOr[oi] | suffixOr[oi + 1];
        for (let t = 0; t < samples; t++) {
          const inn = Math.floor(rng() * m);
          if (sel.includes(inn)) continue;
          const gain = unc - popcount(reachable & ~(wo | sets[inn]));
          if (gain > bestGain) {
            bestGain = gain;
            bestOut = oi;
            bestIn = inn;
          }
        }
      }
      if (bestGain > 0) {
        sel[bestOut] = bestIn;
      } else {
        // Random walk to escape local optima.
        sel[Math.floor(rng() * sel.length)] = Math.floor(rng() * m);
      }
    }
    return null;
  };

  let incumbent = removeRedundant(greedy());
  const debug = typeof process !== "undefined" && process.env?.FP_DEBUG === "1";
  if (debug) console.error(`[dbg] LB=${lowerBound} greedy=${incumbent.length} sets=${sets.length}`);

  // LP dual bound: only worth computing when a gap remains.
  let lp: LpBound | null = null;
  if (incumbent.length > lowerBound) {
    const t0 = Date.now();
    lp = lpDualBound(sets, reachable, incumbent.length);
    if (lp.bound > lowerBound) lowerBound = lp.bound;
    if (debug) console.error(`[dbg] LP bound=${lp.bound} value=${lp.value.toFixed(3)} ${Date.now() - t0}ms`);
  }

  for (let k = lowerBound; k < incumbent.length; k++) {
    const t0 = Date.now();
    const w = localSearchCover(k, 60_000);
    if (debug) console.error(`[dbg] localSearch k=${k}: ${w ? "found" : "failed"} ${Date.now() - t0}ms`);
    if (w) {
      incumbent = removeRedundant([...new Set(w)]);
      break;
    }
  }

  const stats: SearchStat[] = [];
  let solution: number[] | null = null;

  if (incumbent.length > lowerBound) {
    // Exact phase: close the gap by proving every k in [lowerBound,
    // incumbent) infeasible, or finding a smaller cover.
    for (let k = Math.max(1, lowerBound); k < incumbent.length; k++) {
    const stat: SearchStat = {
      target: k, nodes: 0, prunedByBound: 0, memoHits: 0, failedStates: 0, solved: false,
    };
    const failed = new Map<string, number>();
    const prefix: number[] = [];

    const rec = (uncovered: bigint, budget: number): number[] | null => {
      if (uncovered === 0n) return [];
      if (budget === 0) return null;
      stat.nodes++;
      if (lowerBoundOf(uncovered, budget) > budget || cliquePrune(uncovered, budget)) {
        stat.prunedByBound++;
        return null;
      }
      const { members, cellOf, sig } = cellsOf(prefix);
      const memoKey = sig + "#" + uncovered.toString(36);
      const known = failed.get(memoKey);
      if (known !== undefined && known >= budget) {
        stat.memoHits++;
        return null;
      }
      // If some uncovered combo involves only features in singleton cells, it
      // is invariant under the stabilizer, so every completion has a canonical
      // config covering it: branch on the rarest such combo (MRV). Otherwise
      // branch on the choice of the next canonical config directly.
      let bestE = -1;
      let bestCount = Infinity;
      for (const e of bitsOf(uncovered)) {
        const p = e >> 2;
        const fi = Math.floor(p / n);
        const fj = p % n;
        if (members[cellOf[fi]].length !== 1 || members[cellOf[fj]].length !== 1) continue;
        const c = staticCount(e);
        if (c < bestCount) {
          bestCount = c;
          bestE = e;
          if (c === 1) break;
        }
      }
      // Candidates: canonical configs (covering bestE when set), most new
      // coverage first; skip a candidate whose new coverage is a subset of an
      // already-tried candidate's (if the superset branch failed, the subset
      // branch cannot succeed either).
      const pool = bestE >= 0 ? elemSets.get(bestE)! : sets.map((_, i) => i);
      const cands: { s: number; bits: bigint; add: number }[] = [];
      for (const s of pool) {
        if (!isCanonical(setConfigs[s], members)) continue;
        const bits = sets[s] & uncovered;
        if (bits !== 0n) cands.push({ s, bits, add: popcount(bits) });
      }
      cands.sort((a, b) => b.add - a.add);
      const tried: bigint[] = [];
      for (const { s, bits } of cands) {
        let dominated = false;
        for (const t of tried) {
          if ((bits & ~t) === 0n) {
            dominated = true;
            break;
          }
        }
        if (dominated) continue;
        tried.push(bits);
        prefix.push(setConfigs[s]);
        const sub = rec(uncovered & ~sets[s], budget - 1);
        prefix.pop();
        if (sub) return [s, ...sub];
      }
      if (failed.size < MEMO_CAP) {
        failed.set(memoKey, budget);
        stat.failedStates++;
      }
      return null;
    };

    stat.nodes++;
    const root = cellsOf([]);
    for (let s = 0; s < sets.length; s++) {
      if ((setConfigs[s] & flippableMask) !== 0) continue;
      if (!isCanonical(setConfigs[s], root.members)) continue;
      prefix.push(setConfigs[s]);
      const sub = rec(reachable & ~sets[s], k - 1);
      prefix.pop();
      if (sub) {
        solution = [s, ...sub];
        break;
      }
    }
    stat.solved = solution !== null;
    stats.push(stat);
    if (debug) console.error(`[dbg] B&B k=${k}: solved=${stat.solved} nodes=${stat.nodes}`);
    if (solution) break;
  }
  }

  const finalSets = solution ?? incumbent;
  const configs = finalSets.map((s) => setConfigs[s]);

  // Self-verification: every chosen config is valid, and their masks cover
  // every reachable pair-combo.
  let covered = 0n;
  let verified = true;
  for (const c of configs) {
    if (!isValidConfig(c, constraints)) verified = false;
    covered |= configMask(c, n);
  }
  if ((covered & reachable) !== reachable) verified = false;

  return {
    n, validConfigs, reachable, reachableCount,
    minCount: configs.length, configs, lowerBound, structural,
    lp: lp ? { bound: lp.bound, value: lp.value } : null,
    boundDetails, stats, verified,
  };
}
