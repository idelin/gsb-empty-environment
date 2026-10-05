import { MinHeap } from "./heap.ts";
import { DEPOT, simulate } from "./simulate.ts";
import type { Move, Plan, Scenario } from "./types.ts";

interface State {
  key: string;
  g: number;
  pos: number;
  load: number;
  depotLeft: number;
  inv: number[];
}

export interface SolveOptions {
  /** exact：枚举所有转移量；candidates：只取断点转移量（快，适合 6-8 站）；
   *  auto（默认）：按估计状态空间规模自动选择 */
  transferMode?: "exact" | "candidates" | "auto";
  debug?: boolean;
}

/** auto 模式下使用 exact 的状态空间规模阈值 */
const EXACT_STATE_LIMIT = 1e8;

/** 每搬运一辆车的微小成本：仅为打破平局，避免“终点装一车车”之类的退化方案 */
const HANDLING_EPS = 1e-6;

/** Floyd-Warshall 求全源最短路；之后车辆只沿最短路行驶，途经站不再停留。 */
function shortestPaths(dist: number[][]): number[][] {
  const n = dist.length;
  const d = dist.map((row) => row.slice());
  for (let k = 0; k < n; k++)
    for (let i = 0; i < n; i++)
      for (let j = 0; j < n; j++) {
        const v = d[i][k] + d[k][j];
        if (v < d[i][j]) d[i][j] = v;
      }
  return d;
}

/**
 * 求解静态共享单车调度问题（站点数 <= 8，允许多次访问，车辆必须回仓）。
 *
 * 状态 = (车辆位置, 车载库存, 仓库余量, 各站库存)，在状态图上做 A* 搜索，
 * 目标为“里程成本 + 期末缺车罚金”最小。加速手段（均不损失精确性）：
 *  1. 最短路化简：站点间恒走最短路，去掉“只路过不装卸”的移动；
 *  2. 贪心可行解作为上界；
 *  3. 可采纳启发 h = 回仓最短里程 + 不可修复缺车罚金下界。
 * 状态转移按时间顺序施加、取车受站点当前库存限制，因此天然满足
 * “不能通过负库存提前取走尚未送达的车”。
 *
 * transferMode = "candidates" 时只枚举断点转移量（取空/放满车辆、
 * 站点达到目标/空/满），状态空间大幅缩小，适合 6-8 站；此时结果为
 * 高质量近似解（实践中与精确解一致或极接近）。
 */
export function solve(sc: Scenario, opts: SolveOptions = {}): Plan {
  checkScenario(sc);
  const n = sc.stations.length;
  const cap = sc.vehicle.capacity;
  const perKm = sc.costs.perKm;
  const overagePerBike = sc.costs.overagePerBike ?? 0;
  const depotSupply = sc.depot?.supply ?? Number.POSITIVE_INFINITY;
  const canUnload = sc.depot?.canUnload ?? true;
  const maxIter = sc.maxIterations ?? 2_000_000;
  const mode = resolveMode(sc, opts.transferMode ?? sc.transferMode ?? "auto");
  const d = shortestPaths(sc.distanceMatrix);

  const penaltyOf = (inv: number[]): number => {
    let p = 0;
    for (let i = 0; i < n; i++) {
      const t = sc.stations[i].target;
      if (inv[i] < t) p += (t - inv[i]) * sc.costs.shortagePerBike;
      else p += (inv[i] - t) * overagePerBike;
    }
    return p;
  };

  const totalNeedOf = (inv: number[]): number => {
    let s = 0;
    for (let i = 0; i < n; i++) s += Math.max(0, sc.stations[i].target - inv[i]);
    return s;
  };

  /** 可采纳启发：剩余成本下界（回仓里程 + 无论如何都补不上的缺车罚金）。 */
  const heuristic = (pos: number, load: number, depotLeft: number, inv: number[]): number => {
    let need = 0;
    let surplus = 0;
    for (let i = 0; i < n; i++) {
      const t = sc.stations[i].target;
      if (inv[i] < t) need += t - inv[i];
      else surplus += inv[i] - t;
    }
    const unfixable = Math.max(0, need - load - surplus - depotLeft);
    return d[pos][DEPOT] * perKm + unfixable * sc.costs.shortagePerBike;
  };

  const encode = (pos: number, load: number, depotLeft: number, inv: number[]): string =>
    `${pos}|${load}|${depotLeft === Number.POSITIVE_INFINITY ? "I" : depotLeft}|${inv.join(",")}`;

  const startInv = sc.stations.map((s) => s.initial);
  const startLoad = sc.vehicle.initialLoad ?? 0;
  const startKey = encode(DEPOT, startLoad, depotSupply, startInv);

  // 上界：初始状态的罚金 与 贪心可行解 取较小者
  const greedy = greedyBound(sc, d);
  let best = Math.min(penaltyOf(startInv), greedy);
  let bestKey: string | null = penaltyOf(startInv) <= greedy ? startKey : null;

  const gScore = new Map<string, number>([[startKey, 0]]);
  const parent = new Map<string, { prev: string; move: Move }>();
  const heap = new MinHeap<State>();
  heap.push(
    { key: startKey, g: 0, pos: DEPOT, load: startLoad, depotLeft: depotSupply, inv: startInv },
    heuristic(DEPOT, startLoad, depotSupply, startInv),
  );

  let iter = 0;

  const relax = (cur: State, to: number, transfer: number, g2: number, inv2: number[]): void => {
    const load2 = cur.load - transfer;
    let depotLeft2 = cur.depotLeft;
    if (to === DEPOT && Number.isFinite(cur.depotLeft)) depotLeft2 = cur.depotLeft + transfer;
    const key2 = encode(to, load2, depotLeft2, inv2);
    if (g2 >= (gScore.get(key2) ?? Number.POSITIVE_INFINITY)) return;
    gScore.set(key2, g2);
    parent.set(key2, {
      prev: cur.key,
      move: { from: cur.pos, to, transfer, distance: d[cur.pos][to] },
    });
    heap.push(
      { key: key2, g: g2, pos: to, load: load2, depotLeft: depotLeft2, inv: inv2 },
      g2 + heuristic(to, load2, depotLeft2, inv2),
    );
  };

  /** 站点处的转移量候选。candidates 模式只保留断点值。 */
  const stationTransfers = (s: number, load: number, invS: number): number[] => {
    const maxDrop = Math.min(load, sc.stations[s].capacity - invS);
    const maxPick = Math.min(cap - load, invS);
    if (mode === "exact") {
      const ts: number[] = [];
      for (let t = -maxPick; t <= maxDrop; t++) if (t !== 0) ts.push(t);
      return ts;
    }
    const set = new Set<number>();
    if (maxDrop > 0) set.add(maxDrop); // 放满站点或放空车
    if (maxPick > 0) set.add(-maxPick); // 取空站点或装满车
    const hit = sc.stations[s].target - invS; // 恰好达到目标
    if (hit !== 0 && hit >= -maxPick && hit <= maxDrop) set.add(hit);
    return [...set];
  };

  /** 仓库处的转移量候选。 */
  const depotTransfers = (load: number, depotLeft: number, inv: number[]): number[] => {
    const maxUnload = canUnload ? load : 0;
    const maxLoad = Math.min(cap - load, depotLeft);
    if (mode === "exact") {
      const ts: number[] = [0]; // 允许空手回仓
      for (let t = -maxLoad; t <= maxUnload; t++) if (t !== 0) ts.push(t);
      return ts;
    }
    const set = new Set<number>([0]);
    if (maxUnload > 0) set.add(maxUnload);
    if (maxLoad > 0) {
      set.add(-maxLoad);
      const need = Math.min(maxLoad, totalNeedOf(inv));
      if (need > 0) set.add(-need); // 恰好装够剩余需求
    }
    return [...set];
  };

  while (heap.size > 0) {
    const cur = heap.pop()!;
    const knownG = gScore.get(cur.key);
    if (knownG === undefined || cur.g > knownG) continue; // 过期堆项
    const f = cur.g + heuristic(cur.pos, cur.load, cur.depotLeft, cur.inv);
    if (f >= best) break; // 之后任何目标的总成本都不可能更优
    if (++iter > maxIter) {
      throw new Error(`状态搜索超过上限 ${maxIter}，请调大 maxIterations 或改用 candidates 模式`);
    }
    if (opts.debug && iter % 100_000 === 0) {
      console.error(`iter=${iter} heap=${heap.size} best=${best.toFixed(1)}`);
    }

    if (cur.pos === DEPOT) {
      const total = cur.g + penaltyOf(cur.inv);
      if (total < best) {
        best = total;
        bestKey = cur.key;
      }
    }

    for (let to = 0; to <= n; to++) {
      if (to === cur.pos) continue;
      const leg = d[cur.pos][to] * perKm;
      if (cur.g + leg >= best) continue; // 即使罚金清零也不可能更优

      if (to === DEPOT) {
        for (const t of depotTransfers(cur.load, cur.depotLeft, cur.inv)) {
          relax(cur, to, t, cur.g + leg + Math.abs(t) * HANDLING_EPS, cur.inv);
        }
      } else {
        const s = to - 1;
        for (const t of stationTransfers(s, cur.load, cur.inv[s])) {
          const inv2 = cur.inv.slice();
          inv2[s] += t;
          relax(cur, to, t, cur.g + leg + Math.abs(t) * HANDLING_EPS, inv2);
        }
      }
    }
  }

  if (bestKey === null) {
    // 上界来自贪心且搜索未能改进：贪心解即所求（exact 模式下已证明最优）
    return simulate(sc, greedyMoves(sc, d)).plan;
  }

  const moves: Move[] = [];
  for (let k: string | null = bestKey; k && k !== startKey; ) {
    const p = parent.get(k)!;
    moves.push(p.move);
    k = p.prev;
  }
  moves.reverse();

  // 重新模拟一遍：既生成时间线，也校验全部硬约束与成本口径
  return simulate(sc, moves).plan;
}

/** 贪心构造一份可行方案（作为上界）：有车就送缺车站，没车就去盈余站/仓库取。 */
function greedyMoves(sc: Scenario, d: number[][]): Move[] {
  const n = sc.stations.length;
  const inv = sc.stations.map((s) => s.initial);
  let load = sc.vehicle.initialLoad ?? 0;
  let depotLeft = sc.depot?.supply ?? Number.POSITIVE_INFINITY;
  let pos = DEPOT;
  const moves: Move[] = [];

  const totalNeed = (): number => {
    let s = 0;
    for (let i = 0; i < n; i++) s += Math.max(0, sc.stations[i].target - inv[i]);
    return s;
  };

  for (let guard = 0; guard < 10_000 && totalNeed() > 0; guard++) {
    if (load > 0) {
      let j = -1;
      for (let i = 0; i < n; i++) {
        if (sc.stations[i].target > inv[i] && (j < 0 || d[pos][i + 1] < d[pos][j + 1])) j = i;
      }
      if (j < 0) break;
      const t = Math.min(load, sc.stations[j].target - inv[j], sc.stations[j].capacity - inv[j]);
      moves.push({ from: pos, to: j + 1, transfer: t, distance: d[pos][j + 1] });
      inv[j] += t;
      load -= t;
      pos = j + 1;
    } else {
      let j = -1;
      for (let i = 0; i < n; i++) {
        if (inv[i] > sc.stations[i].target && (j < 0 || d[pos][i + 1] < d[pos][j + 1])) j = i;
      }
      const depotDist = depotLeft > 0 ? d[pos][DEPOT] : Number.POSITIVE_INFINITY;
      if (j < 0 && !Number.isFinite(depotDist)) break;
      if (j < 0 || depotDist <= d[pos][j + 1]) {
        if (pos !== DEPOT) {
          moves.push({ from: pos, to: DEPOT, transfer: 0, distance: d[pos][DEPOT] });
          pos = DEPOT;
        }
        const t = Math.min(sc.vehicle.capacity, depotLeft, totalNeed());
        moves.push({ from: DEPOT, to: DEPOT, transfer: -t, distance: 0 });
        load += t;
        depotLeft -= t;
      } else {
        const t = Math.min(sc.vehicle.capacity - load, inv[j] - sc.stations[j].target);
        moves.push({ from: pos, to: j + 1, transfer: -t, distance: d[pos][j + 1] });
        inv[j] -= t;
        load += t;
        pos = j + 1;
      }
    }
  }
  if (pos !== DEPOT) moves.push({ from: pos, to: DEPOT, transfer: 0, distance: d[pos][DEPOT] });
  return moves;
}

function greedyBound(sc: Scenario, d: number[][]): number {
  const moves = greedyMoves(sc, d);
  const handling = moves.reduce((s, m) => s + Math.abs(m.transfer), 0) * HANDLING_EPS;
  return simulate(sc, moves).plan.totalCost + handling;
}

/** 求解实际使用的转移量枚举模式（供 CLI 展示）。 */
export function effectiveMode(sc: Scenario): "exact" | "candidates" {
  return resolveMode(sc, sc.transferMode ?? "auto");
}

function resolveMode(
  sc: Scenario,
  pref: "exact" | "candidates" | "auto",
): "exact" | "candidates" {
  if (pref !== "auto") return pref;
  let states = (sc.stations.length + 1) * (sc.vehicle.capacity + 1);
  for (const s of sc.stations) states *= s.capacity + 1;
  return states <= EXACT_STATE_LIMIT ? "exact" : "candidates";
}

function checkScenario(sc: Scenario): void {
  const n = sc.stations.length;
  if (n === 0) throw new Error("至少需要一个站点");
  if (n > 8) throw new Error(`站点数 ${n} 超过上限 8`);
  const m = sc.distanceMatrix;
  if (m.length !== n + 1 || m.some((row) => row.length !== n + 1)) {
    throw new Error(`距离矩阵必须是 ${n + 1}x${n + 1}（含仓库）`);
  }
  sc.stations.forEach((s, i) => {
    if (s.initial < 0 || s.initial > s.capacity) {
      throw new Error(`站点 ${s.name ?? i} 初始库存 ${s.initial} 超出 [0, ${s.capacity}]`);
    }
    if (s.target < 0 || s.target > s.capacity) {
      throw new Error(`站点 ${s.name ?? i} 目标库存 ${s.target} 超出 [0, ${s.capacity}]`);
    }
  });
  if (sc.vehicle.capacity <= 0) throw new Error("车辆载重上限必须为正数");
}
