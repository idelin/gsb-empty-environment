import { MoveStep, Problem, Solution } from './types';

const DEPOT = -1;

/** 搜索状态 */
interface State {
  /** 当前位置：-1 仓库，否则站点下标 */
  pos: number;
  /** 车载库存 */
  load: number;
  /** 各站点当前库存 */
  inv: number[];
  /** 累计行驶里程 */
  km: number;
  /** 到达该状态的路径 */
  steps: MoveStep[];
}

/** 输入校验，不合法时抛出错误信息 */
export function validateProblem(p: Problem): void {
  if (!Number.isFinite(p.depot.x) || !Number.isFinite(p.depot.y)) {
    throw new Error('仓库坐标不合法');
  }
  if (!Number.isInteger(p.vehicleCapacity) || p.vehicleCapacity < 0) {
    throw new Error('车辆载重上限必须是非负整数');
  }
  if (!(p.costPerKm >= 0) || !(p.penaltyPerBike >= 0)) {
    throw new Error('里程成本与缺车罚金必须非负');
  }
  const n = p.stations.length;
  if (n < 1) throw new Error('至少需要一个站点');
  if (n > 8) throw new Error(`站点数最多 8 个，当前为 ${n}`);
  for (const s of p.stations) {
    if (!s.id) throw new Error('站点缺少 id');
    if (!Number.isFinite(s.x) || !Number.isFinite(s.y)) {
      throw new Error(`站点 ${s.id} 坐标不合法`);
    }
    for (const [name, v] of [
      ['容量', s.capacity],
      ['初始库存', s.initial],
      ['次日需求', s.demand],
    ] as const) {
      if (!Number.isInteger(v) || v < 0) {
        throw new Error(`站点 ${s.id} 的${name}必须是非负整数`);
      }
    }
    if (s.initial > s.capacity) {
      throw new Error(`站点 ${s.id} 初始库存 ${s.initial} 超过容量 ${s.capacity}`);
    }
  }
}

/**
 * 生成在某站点值得尝试的装卸量候选。
 * delta > 0 表示卸车（站点库存增加），delta < 0 表示装车。
 * 只枚举“有意义”的取值，避免在整数区间上暴力展开。
 */
function candidateDeltas(
  inv: number,
  capacity: number,
  target: number,
  load: number,
  vehicleCapacity: number,
): number[] {
  const maxUnload = Math.min(load, capacity - inv); // 受车载与站点剩余容量限制
  const maxPickup = Math.min(inv, vehicleCapacity - load); // 受站点现有库存与车辆剩余载重限制
  const set = new Set<number>();
  const add = (d: number): void => {
    const r = Math.round(d);
    if (r >= -maxPickup && r <= maxUnload) set.add(r);
  };
  add(0); // 只路过不装卸
  add(maxUnload); // 尽可能多卸
  add(-maxPickup); // 尽可能多装
  add(target - inv); // 卸/装到恰好满足需求
  add(capacity - inv); // 卸到站点满
  add(-(inv - target)); // 装走超出需求的部分
  return [...set];
}

/**
 * 束搜索求解。
 *
 * 状态空间：(当前位置, 车载库存, 各站库存向量)。
 * 硬约束在状态转移时保证：
 *  - 站点库存始终 ∈ [0, 容量]（不允许负库存，即不能提前取走尚未送达的车）；
 *  - 车载库存始终 ∈ [0, 载重上限]；
 *  - 路线从仓库出发，只有在仓库的状态才作为完整方案（即必须回仓）。
 * 目标：里程成本 + 缺车罚金最小。
 */
export function solve(problem: Problem): Solution {
  validateProblem(problem);

  const n = problem.stations.length;
  const vehCap = problem.vehicleCapacity;
  const targets = problem.stations.map((s) => Math.min(s.demand, s.capacity));
  const maxStops = problem.maxStops ?? 2 * n + 4;
  const beamWidth = problem.beamWidth ?? 4000;

  // 位置表：0 = 仓库，1..n = 站点
  const xs = [problem.depot.x, ...problem.stations.map((s) => s.x)];
  const ys = [problem.depot.y, ...problem.stations.map((s) => s.y)];
  const locOf = (pos: number): number => pos + 1;
  const dist = (a: number, b: number): number => {
    const ia = locOf(a);
    const ib = locOf(b);
    const dx = (xs[ia] as number) - (xs[ib] as number);
    const dy = (ys[ia] as number) - (ys[ib] as number);
    return Math.hypot(dx, dy);
  };

  const shortageOf = (inv: number[]): number[] =>
    inv.map((v, i) => Math.max(0, (targets[i] as number) - v));
  const penaltyOf = (inv: number[]): number =>
    shortageOf(inv).reduce((sum, s) => sum + s, 0) * problem.penaltyPerBike;

  const toSolution = (st: State): Solution => {
    const shortage = shortageOf(st.inv);
    const penalty = shortage.reduce((sum, s) => sum + s, 0) * problem.penaltyPerBike;
    const mileageCost = st.km * problem.costPerKm;
    return {
      steps: st.steps,
      totalKm: st.km,
      mileageCost,
      shortage,
      penalty,
      totalCost: mileageCost + penalty,
    };
  };

  const initial: State = {
    pos: DEPOT,
    load: 0,
    inv: problem.stations.map((s) => s.initial),
    km: 0,
    steps: [],
  };

  // “什么都不做”也是一个合法方案
  let best = toSolution(initial);

  let frontier: State[] = [initial];
  for (let depth = 0; depth < maxStops; depth++) {
    const next = new Map<string, State>();
    const consider = (st: State): void => {
      // 里程成本已超过已知最优方案（罚金非负），剪枝
      if (st.km * problem.costPerKm >= best.totalCost) return;
      const key = `${st.pos}|${st.load}|${st.inv.join(',')}`;
      const old = next.get(key);
      if (old === undefined || st.km < old.km) next.set(key, st);
    };

    for (const st of frontier) {
      for (let to = DEPOT; to < n; to++) {
        if (to === st.pos) continue;
        const km = st.km + dist(st.pos, to);
        if (to === DEPOT) {
          // 回仓：不在仓库装卸，构成一个完整方案，也允许再次出车
          const back: State = {
            pos: DEPOT,
            load: st.load,
            inv: st.inv,
            km,
            steps: [
              ...st.steps,
              {
                from: st.pos,
                to: DEPOT,
                driveKm: km - st.km,
                delta: 0,
                loadAfter: st.load,
                invAfter: st.inv,
              },
            ],
          };
          const sol = toSolution(back);
          if (sol.totalCost < best.totalCost) best = sol;
          consider(back);
          continue;
        }
        const i = to;
        const invI = st.inv[i] as number;
        const stn = problem.stations[i];
        const deltas = candidateDeltas(
          invI,
          stn === undefined ? 0 : stn.capacity,
          targets[i] as number,
          st.load,
          vehCap,
        );
        for (const delta of deltas) {
          const inv2 = st.inv.slice();
          inv2[i] = invI + delta;
          const step: MoveStep = {
            from: st.pos,
            to,
            driveKm: km - st.km,
            delta,
            loadAfter: st.load - delta,
            invAfter: inv2,
          };
          consider({ pos: to, load: st.load - delta, inv: inv2, km, steps: [...st.steps, step] });
        }
      }
    }

    // 按“里程成本 + 当前缺车罚金”排序，保留最优的 beamWidth 个状态
    const scored = [...next.values()].sort(
      (a, b) =>
        a.km * problem.costPerKm + penaltyOf(a.inv) - (b.km * problem.costPerKm + penaltyOf(b.inv)),
    );
    frontier = scored.slice(0, beamWidth);
    if (frontier.length === 0) break;
  }

  return best;
}
