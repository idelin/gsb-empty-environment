/**
 * 精确最小集合覆盖求解：
 *   全集   = 全部"可达"的两两取值组合（非法/不可达组合从不计入）；
 *   候选集 = 全部合法配置（每个配置覆盖它所含的组合）。
 *
 * 最小性证明由三部分构成：
 *   上界：贪心 + 随机重启 + （无约束时的 Kleitman–Spencer 构造）给出一个覆盖；
 *   下界：装箱下界（两两不能同配置的组合集合）与无约束子集的组合下界取最大；
 *   若上下界不相等，则用分支限界做确定性穷举，证明少一个配置不可行。
 */

import type { Clause, CompiledModel } from './model.ts';
import { minRowsUnconstrained } from './clique.ts';

export interface PairReq {
  i: number;
  j: number;
  vi: number;
  vj: number;
}

export interface CoverResult {
  configs: number[];          // 最优配置（位掩码）
  lowerBound: number;         // 根节点下界
  lowerBoundReasons: string[];
  searchNodes: number;        // 分支限界搜索节点数（穷举证明的规模）
  usedExactSearch: boolean;   // 是否需要穷举（下界已等于上界时为 false）
  aborted: boolean;           // 超过节点上限而中止（此时结果未证明最小）
}

export function litSatisfied(lit: number, mask: number): boolean {
  const bit = (mask >> (lit >> 1)) & 1;
  return (lit & 1) === 0 ? bit === 1 : bit === 0;
}

export function isValidConfig(mask: number, clauses: Clause[]): boolean {
  for (const c of clauses) {
    if (!litSatisfied(c.a, mask) && !litSatisfied(c.b, mask)) return false;
  }
  return true;
}

export function enumerateValidConfigs(n: number, clauses: Clause[]): number[] {
  const out: number[] = [];
  for (let m = 0; m < (1 << n); m++) {
    if (isValidConfig(m, clauses)) out.push(m);
  }
  return out;
}

/** 计算全部可达 / 不可达的两两取值组合。 */
export function computePairs(n: number, valid: number[]): { reqs: PairReq[]; unreachable: PairReq[] } {
  const reqs: PairReq[] = [];
  const unreachable: PairReq[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      for (let vi = 0; vi <= 1; vi++) {
        for (let vj = 0; vj <= 1; vj++) {
          const ok = valid.some(m => ((m >> i) & 1) === vi && ((m >> j) & 1) === vj);
          const r: PairReq = { i, j, vi, vj };
          if (ok) reqs.push(r); else unreachable.push(r);
        }
      }
    }
  }
  return { reqs, unreachable };
}

const POP16 = new Uint8Array(65536);
for (let i = 1; i < 65536; i++) POP16[i] = POP16[i >> 1] + (i & 1);

function popcount(x: bigint): number {
  let c = 0;
  while (x !== 0n) { c += POP16[Number(x & 0xffffn)]; x >>= 16n; }
  return c;
}

/** 最低有效位的位置（x ≠ 0）。 */
function lowestBitIndex(x: bigint): number {
  let n = 0;
  while ((x & 0xffffffffn) === 0n) { x >>= 32n; n += 32; }
  const v = Number(x & 0xffffffffn);
  return n + (31 - Math.clz32(v & -v));
}

/** 对 x 的每个置位调用 f（按位下标递增）。 */
function forEachBit(x: bigint, f: (idx: number) => void): void {
  let rest = x;
  while (rest !== 0n) {
    const idx = lowestBitIndex(rest);
    f(idx);
    rest &= rest - 1n;
  }
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface SetEntry {
  config: number;
  cover: bigint;
}

export function solveMinCover(
  model: CompiledModel,
  valid: number[],
  reqs: PairReq[],
  maxNodes = 5_000_000,
): CoverResult {
  const n = model.n;
  const m = reqs.length;
  const reasons: string[] = [];

  if (m === 0) {
    return {
      configs: [valid[0]],
      lowerBound: 1,
      lowerBoundReasons: ['没有任何可达的两两组合，1 个合法配置即为最小。'],
      searchNodes: 0,
      usedExactSearch: false,
      aborted: false,
    };
  }

  // 组合 → 需求编号（数值索引，避免字符串键）
  const pairList: [number, number][] = [];
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) pairList.push([i, j]);
  const pairPos: number[][] = Array.from({ length: n }, () => new Array<number>(n).fill(-1));
  pairList.forEach(([i, j], p) => { pairPos[i][j] = p; });
  const reqIdOf = new Int32Array(pairList.length * 4).fill(-1); // [pairIdx][vi*2+vj]
  reqs.forEach((r, idx) => {
    reqIdOf[pairPos[r.i][r.j] * 4 + r.vi * 2 + r.vj] = idx;
  });

  const coverageOf = (config: number): bigint => {
    let cov = 0n;
    for (let p = 0; p < pairList.length; p++) {
      const [i, j] = pairList[p];
      const vi = (config >> i) & 1, vj = (config >> j) & 1;
      const id = reqIdOf[p * 4 + vi * 2 + vj];
      if (id >= 0) cov |= 1n << BigInt(id);
    }
    return cov;
  };

  // 候选集：合法配置，按覆盖内容去重，丢弃空覆盖
  const byCover = new Map<bigint, number>();
  for (const c of valid) {
    const cov = coverageOf(c);
    if (cov === 0n) continue;
    if (!byCover.has(cov)) byCover.set(cov, c);
  }
  let sets: SetEntry[] = [...byCover.entries()].map(([cover, config]) => ({ config, cover }));

  // 支配消除：cover(a) ⊆ cover(b) 则 a 可去（存在不含 a 的最优解）
  if (sets.length <= 2500) {
    const keep = new Array<boolean>(sets.length).fill(true);
    for (let a = 0; a < sets.length; a++) {
      if (!keep[a]) continue;
      for (let b = 0; b < sets.length; b++) {
        if (a !== b && keep[b] && (sets[a].cover & ~sets[b].cover) === 0n) {
          keep[a] = false;
          break;
        }
      }
    }
    sets = sets.filter((_, i) => keep[i]);
  }

  const full = (1n << BigInt(m)) - 1n;
  const covers = sets.map(s => s.cover);

  // compat[e]：与需求 e 可能被同一配置同时覆盖的全部需求（含 e 自身）
  const compat: bigint[] = new Array<bigint>(m).fill(0n);
  const containers: number[][] = Array.from({ length: m }, () => []);
  covers.forEach((cov, s) => {
    forEachBit(cov, (e) => {
      compat[e] |= cov;
      containers[e].push(s);
    });
  });

  // ---------- 下界 1：无约束特性子集的组合下界 ----------
  const freeFeatures: number[] = [];
  for (let f = 0; f < n; f++) if (!model.constrained[f]) freeFeatures.push(f);
  let combLB = 0;
  // 初始上界：全部候选集（其并集必然覆盖全部可达组合）
  let best: number[] = sets.map((_, i) => i);
  if (freeFeatures.length >= 2) {
    const { rows, columns } = minRowsUnconstrained(freeFeatures.length);
    combLB = rows;
    const names = freeFeatures.map(f => model.features[f]).join(', ');
    reasons.push(
      `组合下界：特性 {${names}} 共 ${freeFeatures.length} 个不受任何约束；` +
      `覆盖其全部两两组合至少需要 ${rows} 个配置` +
      `（N 行二值列的两两覆盖等价于"两两定性独立列族"，精确最大团搜索证明 ${rows - 1} 行最多容纳更少列）。`
    );
    // 完全无约束时，构造本身即最优上界
    if (model.clauses.length === 0) {
      const constructed: number[] = [];
      for (let r = 0; r < rows; r++) {
        let mask = 0;
        for (let f = 0; f < n; f++) {
          if ((columns[f] >> r) & 1) mask |= 1 << f;
        }
        constructed.push(mask);
      }
      const idx = constructed.map(c => sets.findIndex(s => s.config === c));
      if (idx.every(x => x >= 0) && idx.length < best.length) best = idx;
    }
  }

  // ---------- 上界：贪心 + 随机重启（构造已达下界时跳过） ----------
  const greedyOnce = (rand: () => number): number[] => {
    let uncovered = full;
    const chosen: number[] = [];
    while (uncovered !== 0n) {
      let bestGain = 0;
      let bestS: number[] = [];
      for (let s = 0; s < covers.length; s++) {
        const gain = popcount(covers[s] & uncovered);
        if (gain > bestGain) { bestGain = gain; bestS = [s]; }
        else if (gain === bestGain && gain > 0) bestS.push(s);
      }
      const s = bestS[Math.floor(rand() * bestS.length)];
      chosen.push(s);
      uncovered &= ~covers[s];
    }
    // 去冗余
    for (let t = chosen.length - 1; t >= 0; t--) {
      let union = 0n;
      for (let u = 0; u < chosen.length; u++) if (u !== t) union |= covers[chosen[u]];
      if (union === full) chosen.splice(t, 1);
    }
    return chosen;
  };

  if (best.length > combLB) {
    const restarts = covers.length > 1500 ? 25 : 200;
    let gBest = greedyOnce(() => 0);
    for (let r = 0; r < restarts; r++) {
      const cand = greedyOnce(mulberry32(r + 1));
      if (cand.length < gBest.length) gBest = cand;
    }
    if (gBest.length < best.length) best = gBest;
  }

  // ---------- 下界 2：装箱下界（两两不能同配置的需求集合） ----------
  const packingWitness: number[] = [];
  const packingLB = (uncovered: bigint, witness: number[] | null): number => {
    let t = uncovered;
    let c = 0;
    while (t !== 0n) {
      const idx = lowestBitIndex(t);
      c++;
      if (witness) witness.push(idx);
      t &= ~compat[idx];
    }
    return c;
  };
  const packRoot = packingLB(full, packingWitness);
  if (packRoot > 0) {
    const fmt = (e: number) => {
      const r = reqs[e];
      return `${model.features[r.i]}=${r.vi}∧${model.features[r.j]}=${r.vj}`;
    };
    const shown = packingWitness.slice(0, 8).map(fmt).join('、');
    const more = packingWitness.length > 8 ? ` 等 ${packRoot} 个` : '';
    reasons.push(
      `装箱下界：存在 ${packRoot} 个可达组合，其中任意两个都无法被同一合法配置同时覆盖，` +
      `每个都至少需要 1 个配置 ⇒ 至少 ${packRoot} 个（${shown}${more}）。`
    );
  }

  const lowerBound = Math.max(combLB, packRoot);

  // ---------- 分支限界穷举（若下界已等于上界则立即结束） ----------
  let nodes = 0;
  let usedExactSearch = false;
  let aborted = false;
  const failMemo = new Map<bigint, number>();

  const lowerBoundAt = (uncovered: bigint): number => {
    // 装箱下界
    let t = uncovered;
    let pack = 0;
    while (t !== 0n) {
      const idx = lowestBitIndex(t);
      pack++;
      t &= ~compat[idx];
    }
    // 最大新增覆盖下界
    let maxNew = 1;
    for (let s = 0; s < covers.length; s++) {
      const g = popcount(covers[s] & uncovered);
      if (g > maxNew) maxNew = g;
    }
    const countLB = Math.ceil(popcount(uncovered) / maxNew);
    return Math.max(pack, countLB);
  };

  const chosen: number[] = [];
  const bnb = (uncovered: bigint): void => {
    nodes++;
    if (uncovered === 0n) {
      if (chosen.length < best.length) best = chosen.slice();
      return;
    }
    const budget = best.length - chosen.length;
    if (budget <= 0) return;
    if (lowerBoundAt(uncovered) >= budget) return;
    const failed = failMemo.get(uncovered);
    if (failed !== undefined && failed >= budget) return;
    if (nodes > maxNodes) { aborted = true; return; }

    // 选候选集最少的需求分支
    let branchCands: number[] | null = null;
    forEachBit(uncovered, (e) => {
      if (branchCands !== null && branchCands.length <= 1) return;
      const cands = containers[e].filter(s => (covers[s] & uncovered) !== 0n);
      if (branchCands === null || cands.length < branchCands.length) {
        branchCands = cands;
      }
    });
    // 覆盖新需求多者优先
    branchCands!.sort((s1, s2) => popcount(covers[s2] & uncovered) - popcount(covers[s1] & uncovered));
    for (const s of branchCands!) {
      chosen.push(s);
      bnb(uncovered & ~covers[s]);
      chosen.pop();
      if (aborted) return;
    }
    failMemo.set(uncovered, budget);
  };

  if (lowerBound < best.length) {
    usedExactSearch = true;
    bnb(full);
  }

  return {
    configs: best.map(s => sets[s].config),
    lowerBound,
    lowerBoundReasons: reasons,
    searchNodes: nodes,
    usedExactSearch,
    aborted,
  };
}
