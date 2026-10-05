import type { Move, Plan, Scenario } from "./types.ts";

export const DEPOT = 0;

/** 时间线中的一行：某次移动/装卸完成后的快照。 */
export interface TimelineRow {
  step: number;
  /** 当前所在位置，0 = 仓库 */
  location: number;
  /** 在该位置转移的车辆数（>0 放下，<0 取走，0 仅途经/回仓） */
  transfer: number;
  /** 累计里程（公里） */
  distance: number;
  /** 操作后的车载库存 */
  load: number;
  /** 操作后的各站库存 */
  inventories: number[];
}

export interface Simulation {
  rows: TimelineRow[];
  plan: Plan;
}

/**
 * 按顺序模拟一份调度方案，严格校验全部硬约束：
 *  - 车载不能为负、不能超过载重上限；
 *  - 站点库存不能为负（即不能提前取走尚未送达的车）、不能超过站点容量；
 *  - 仓库供应量有限时不能超取；
 *  - 车辆最终必须回到仓库。
 * 任何违反都会抛出异常。
 */
export function simulate(sc: Scenario, moves: Move[]): Simulation {
  const n = sc.stations.length;
  const inv = sc.stations.map((s) => s.initial);
  let load = sc.vehicle.initialLoad ?? 0;
  let depotLeft = sc.depot?.supply ?? Number.POSITIVE_INFINITY;
  const canUnload = sc.depot?.canUnload ?? true;
  let pos = DEPOT;
  let cum = 0;

  if (load < 0 || load > sc.vehicle.capacity) {
    throw new Error(`初始车载 ${load} 超出 [0, ${sc.vehicle.capacity}]`);
  }

  const rows: TimelineRow[] = [
    { step: 0, location: DEPOT, transfer: 0, distance: 0, load, inventories: [...inv] },
  ];

  moves.forEach((m, i) => {
    const step = i + 1;
    if (m.from !== pos) {
      throw new Error(`第 ${step} 步：起点 ${m.from} 与当前位置 ${pos} 不连续`);
    }
    pos = m.to;
    cum += m.distance;
    load -= m.transfer;

    if (m.to === DEPOT) {
      if (m.transfer > 0 && !canUnload) throw new Error(`第 ${step} 步：仓库不允许卸车`);
      depotLeft += m.transfer; // 卸车回补仓库，装车扣减仓库
      if (depotLeft < 0) throw new Error(`第 ${step} 步：仓库备货不足，不能装车`);
    } else {
      const s = m.to - 1;
      inv[s] += m.transfer;
      if (inv[s] < 0) {
        throw new Error(
          `第 ${step} 步：站点 ${sc.stations[s].name} 出现负库存，不能提前取走尚未送达的车`,
        );
      }
      if (inv[s] > sc.stations[s].capacity) {
        throw new Error(
          `第 ${step} 步：站点 ${sc.stations[s].name} 库存 ${inv[s]} 超过容量 ${sc.stations[s].capacity}`,
        );
      }
    }
    if (load < 0) throw new Error(`第 ${step} 步：车载库存为负`);
    if (load > sc.vehicle.capacity) {
      throw new Error(`第 ${step} 步：车载 ${load} 超过载重上限 ${sc.vehicle.capacity}`);
    }

    rows.push({ step, location: pos, transfer: m.transfer, distance: cum, load, inventories: [...inv] });
  });

  if (pos !== DEPOT) throw new Error("车辆最终必须回到仓库");

  let shortage = 0;
  let overage = 0;
  let penalty = 0;
  for (let i = 0; i < n; i++) {
    const t = sc.stations[i].target;
    if (inv[i] < t) {
      shortage += t - inv[i];
      penalty += (t - inv[i]) * sc.costs.shortagePerBike;
    } else {
      overage += inv[i] - t;
      penalty += (inv[i] - t) * (sc.costs.overagePerBike ?? 0);
    }
  }
  const mileageCost = cum * sc.costs.perKm;

  return {
    rows,
    plan: {
      moves,
      distanceKm: cum,
      mileageCost,
      shortageBikes: shortage,
      overageBikes: overage,
      penaltyCost: penalty,
      totalCost: mileageCost + penalty,
      finalInventories: inv,
    },
  };
}
