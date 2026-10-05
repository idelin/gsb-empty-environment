/**
 * 物流中转规划器
 * ----------------
 * 给定有限班次表（车辆航段），把最多 10 批货物从起点运到终点，最小化总费用。
 *
 * 模型要点：
 * - 每个终端有固定处理时间 handlingMin：货物到达后不能立即转运，
 *   必须满足 下一班次发车时刻 >= 货物可用时刻 + 处理时间。
 *   在起点，货物可用时刻 = ready（备货完成时刻，绝对分钟）。
 * - 时刻一律使用“绝对分钟”（从第 1 天 00:00 起算），天然支持跨午夜。
 * - 多批货可合车：同一班次上各批重量之和不得超过班次容量。
 * - 班次费用 = 固定成本 cost（启用即收，合车摊薄）+ costPerUnit * 实际载重（可缺省为 0）。
 * - 目标：所有批次都送达，总费用最小。
 *
 * 算法：
 * 1) 班次时间严格递增 => 换乘图是 DAG。对每批货 DFS 枚举全部时间可行路径。
 * 2) 分支限界：为每批货选一条路径，目标为“已启用班次的固定成本并集 + 各批重量附加费”，
 *    容量约束在合并时检查。批次 <= 10，精确求解。
 */

export interface Terminal {
  id: string;
  name: string;
  handlingMin: number; // 固定处理时间（分钟）
}

export interface Trip {
  id: string;
  from: string; // 终端 id
  to: string; // 终端 id
  depart: number; // 绝对分钟
  arrive: number; // 绝对分钟，可跨午夜（> depart）
  capacity: number; // 载重上限
  cost: number; // 启用该班次的固定成本
  costPerUnit?: number; // 每单位重量附加费，缺省 0
}

export interface Batch {
  id: string;
  origin: string;
  dest: string;
  weight: number;
  ready: number; // 起点可用时刻（绝对分钟）
}

export interface Problem {
  terminals: Terminal[];
  trips: Trip[];
  batches: Batch[]; // <= 10
}

export interface Plan {
  ok: boolean;
  totalCost: number;
  reason?: string; // 失败原因
  assignment: Map<string, Trip[]>; // batchId -> 路径
  usedTrips: Map<string, { trip: Trip; batches: Batch[] }>; // tripId -> 装载情况
}

const MAX_PATHS_PER_BATCH = 20000;

/** 绝对分钟 -> "第d天 hh:mm（绝对 m 分）" */
export function fmtTime(m: number): string {
  const day = Math.floor(m / 1440) + 1;
  const hh = String(Math.floor((m % 1440) / 60)).padStart(2, "0");
  const mm = String(m % 60).padStart(2, "0");
  return `第${day}天 ${hh}:${mm}（绝对 ${m} 分）`;
}

/** 枚举某批货的全部时间可行路径（换乘图是 DAG，DFS 必终止） */
function enumeratePaths(
  batch: Batch,
  tripsByFrom: Map<string, Trip[]>,
  terminalById: Map<string, Terminal>
): Trip[][] {
  const paths: Trip[][] = [];
  const cur: Trip[] = [];

  const dfs = (terminal: string, availAt: number) => {
    if (paths.length >= MAX_PATHS_PER_BATCH) return;
    const handling = terminalById.get(terminal)!.handlingMin;
    for (const trip of tripsByFrom.get(terminal) ?? []) {
      // 关键约束：到站时刻不是立即可转运，必须留出固定处理时间
      if (trip.depart < availAt + handling) continue;
      cur.push(trip);
      if (trip.to === batch.dest) {
        paths.push([...cur]);
      } else {
        dfs(trip.to, trip.arrive);
      }
      cur.pop();
    }
  };

  dfs(batch.origin, batch.ready);
  // Standalone 费用低的路径优先，加速分支限界收敛
  const standalone = (p: Trip[]) =>
    p.reduce((s, t) => s + t.cost + (t.costPerUnit ?? 0) * batch.weight, 0);
  paths.sort((a, b) => standalone(a) - standalone(b));
  return paths;
}

/** 精确求解：为每批货选路径，最小化启用班次费用并集 + 重量附加费 */
export function solve(problem: Problem): Plan {
  const terminalById = new Map(problem.terminals.map((t) => [t.id, t]));
  const tripsByFrom = new Map<string, Trip[]>();
  for (const trip of problem.trips) {
    if (!tripsByFrom.has(trip.from)) tripsByFrom.set(trip.from, []);
    tripsByFrom.get(trip.from)!.push(trip);
  }
  for (const list of tripsByFrom.values()) list.sort((a, b) => a.depart - b.depart);

  if (problem.batches.length > 10) {
    return { ok: false, totalCost: 0, reason: "批次超过 10", assignment: new Map(), usedTrips: new Map() };
  }

  // 1) 枚举每批货的可行路径
  const pathsOf = new Map<string, Trip[][]>();
  for (const batch of problem.batches) {
    const paths = enumeratePaths(batch, tripsByFrom, terminalById);
    if (paths.length === 0) {
      return {
        ok: false,
        totalCost: 0,
        reason: `批次 ${batch.id} 无可行路径`,
        assignment: new Map(),
        usedTrips: new Map(),
      };
    }
    pathsOf.set(batch.id, paths);
  }

  // 2) 分支限界。路径少的批次先决策，剪枝更狠。
  const batches = [...problem.batches].sort(
    (a, b) => pathsOf.get(a.id)!.length - pathsOf.get(b.id)!.length
  );
  // 重量附加费的下界（每批必付，与共享无关），用于剪枝
  const minVarCost = new Map<string, number>();
  for (const b of batches) {
    minVarCost.set(
      b.id,
      Math.min(...pathsOf.get(b.id)!.map((p) => p.reduce((s, t) => s + (t.costPerUnit ?? 0) * b.weight, 0)))
    );
  }
  const suffixLB: number[] = new Array(batches.length + 1).fill(0);
  for (let i = batches.length - 1; i >= 0; i--) {
    suffixLB[i] = suffixLB[i + 1] + minVarCost.get(batches[i].id)!;
  }

  const load = new Map<string, number>(); // tripId -> 已装重量
  const chosen = new Map<string, Trip[]>(); // batchId -> 路径
  let bestCost = Infinity;
  let bestAssignment: Map<string, Trip[]> | null = null;

  const dfs = (i: number, cost: number) => {
    if (cost + suffixLB[i] >= bestCost) return;
    if (i === batches.length) {
      bestCost = cost;
      bestAssignment = new Map(chosen);
      return;
    }
    const batch = batches[i];
    for (const path of pathsOf.get(batch.id)!) {
      // 容量检查：合车不得超载
      let feasible = true;
      for (const trip of path) {
        if ((load.get(trip.id) ?? 0) + batch.weight > trip.capacity) {
          feasible = false;
          break;
        }
      }
      if (!feasible) continue;
      // 边际费用：新启用班次收固定成本；重量附加费每批都收
      let marginal = 0;
      for (const trip of path) {
        if (!load.has(trip.id)) marginal += trip.cost;
        marginal += (trip.costPerUnit ?? 0) * batch.weight;
        load.set(trip.id, (load.get(trip.id) ?? 0) + batch.weight);
      }
      chosen.set(batch.id, path);
      dfs(i + 1, cost + marginal);
      chosen.delete(batch.id);
      for (const trip of path) {
        const w = load.get(trip.id)! - batch.weight;
        if (w === 0) load.delete(trip.id);
        else load.set(trip.id, w);
      }
    }
  };
  dfs(0, 0);

  if (!bestAssignment) {
    return {
      ok: false,
      totalCost: 0,
      reason: "容量约束下无可行方案（各批单独可行，但合车超载）",
      assignment: new Map(),
      usedTrips: new Map(),
    };
  }

  // 汇总每班次实际装载
  const usedTrips = new Map<string, { trip: Trip; batches: Batch[] }>();
  for (const batch of problem.batches) {
    for (const trip of bestAssignment.get(batch.id)!) {
      if (!usedTrips.has(trip.id)) usedTrips.set(trip.id, { trip, batches: [] });
      usedTrips.get(trip.id)!.batches.push(batch);
    }
  }
  return { ok: true, totalCost: bestCost, assignment: bestAssignment, usedTrips };
}

/** 输出每次装卸与衔接（全部使用绝对分钟） */
export function renderPlan(problem: Problem, plan: Plan): string {
  if (!plan.ok) return `无可行方案：${plan.reason}`;
  const terminalById = new Map(problem.terminals.map((t) => [t.id, t]));
  const name = (id: string) => terminalById.get(id)?.name ?? id;
  const out: string[] = [];
  out.push(`最小总费用：${plan.totalCost}`);
  out.push("");

  // 时间轴：所有装车 / 卸货事件按绝对分钟排序
  interface Ev {
    time: number;
    text: string;
  }
  const events: Ev[] = [];
  const used = [...plan.usedTrips.values()].sort((a, b) => a.trip.depart - b.trip.depart);
  for (const { trip, batches } of used) {
    const weight = batches.reduce((s, b) => s + b.weight, 0);
    const ids = batches.map((b) => b.id).join("+");
    events.push({
      time: trip.depart,
      text:
        `[装车] ${fmtTime(trip.depart)} ${name(trip.from)}：批次 ${ids} 装上班次 ${trip.id}` +
        `（${name(trip.from)}→${name(trip.to)}，载重 ${weight}/${trip.capacity}）`,
    });
    events.push({
      time: trip.arrive,
      text: `[卸货] ${fmtTime(trip.arrive)} ${name(trip.to)}：班次 ${trip.id} 到达，卸下批次 ${ids}`,
    });
  }
  events.sort((a, b) => a.time - b.time);
  out.push("== 装卸时间轴 ==");
  for (const e of events) out.push(e.text);
  out.push("");

  // 每批货的衔接明细：到达 -> 处理 -> 下一班发车
  out.push("== 各批衔接明细 ==");
  for (const batch of problem.batches) {
    const path = plan.assignment.get(batch.id)!;
    out.push(
      `批次 ${batch.id}（${name(batch.origin)}→${name(batch.dest)}，重 ${batch.weight}，` +
        `就绪 ${fmtTime(batch.ready)}）：`
    );
    let prevArrive: number | null = null;
    for (const trip of path) {
      const handling = terminalById.get(trip.from)!.handlingMin;
      const avail = prevArrive === null ? batch.ready : prevArrive;
      const slack = trip.depart - avail - handling;
      out.push(
        `  在 ${name(trip.from)} 处理 ${handling} 分（衔接余量 ${slack} 分），` +
          `乘 ${trip.id}：${fmtTime(trip.depart)} 发车 -> ${fmtTime(trip.arrive)} 到达 ${name(trip.to)}`
      );
      prevArrive = trip.arrive;
    }
    out.push(`  全部到达：${fmtTime(prevArrive!)}`);
  }
  return out.join("\n");
}

/* ------------------------------ 示例 ------------------------------ */

export const demo: Problem = {
  terminals: [
    { id: "A", name: "甲港", handlingMin: 30 },
    { id: "B", name: "乙枢纽", handlingMin: 45 },
    { id: "C", name: "丙枢纽", handlingMin: 60 },
    { id: "D", name: "丁城", handlingMin: 20 },
  ],
  trips: [
    // 跨午夜班次：第1天 23:10 发，第2天 02:30 到
    { id: "T1", from: "A", to: "B", depart: 1390, arrive: 1590, capacity: 100, cost: 300 },
    { id: "T2", from: "A", to: "C", depart: 600, arrive: 840, capacity: 80, cost: 260 },
    { id: "T3", from: "C", to: "D", depart: 900, arrive: 1080, capacity: 90, cost: 200 },
    // 紧衔接陷阱：T2 840 到 C，处理 60 分 => 900 才可用，赶不上 895 的 T4
    { id: "T4", from: "C", to: "D", depart: 895, arrive: 1000, capacity: 90, cost: 120 },
    { id: "T5", from: "B", to: "D", depart: 1680, arrive: 1860, capacity: 120, cost: 280 },
    { id: "T6", from: "A", to: "D", depart: 1500, arrive: 1980, capacity: 60, cost: 700 },
    { id: "T7", from: "C", to: "D", depart: 1200, arrive: 1380, capacity: 50, cost: 150, costPerUnit: 1 },
  ],
  batches: [
    { id: "B1", origin: "A", dest: "D", weight: 40, ready: 480 },
    { id: "B2", origin: "A", dest: "D", weight: 35, ready: 500 },
    { id: "B3", origin: "A", dest: "D", weight: 30, ready: 1320 },
    { id: "B4", origin: "C", dest: "D", weight: 45, ready: 700 },
  ],
};

declare const require: any;
declare const module: any;
if (typeof require !== "undefined" && require.main === module) {
  const plan = solve(demo);
  console.log(renderPlan(demo, plan));
}
