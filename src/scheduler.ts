import { MinCostFlow, type FlowEdge } from "./minCostFlow.ts";
import type {
  ChargePlan,
  SlotAllocation,
  StationProfile,
  UnmetDemand,
  VehicleInput,
  VehicleSchedule,
  WindowDeficit,
} from "./types.ts";

/** 区间缺口诊断使用 O(T^2) 网格，超出该规模时跳过区间诊断（仍输出逐车缺口）。 */
const DEFICIT_GRID_SLOT_LIMIT = 4096;

interface Run {
  start: number;
  end: number; // 不含
  energy: number;
}

/**
 * 充电调度器：在整数时间格上求按时满足全部车辆需求的最低费用计划。
 *
 * 两阶段：
 * 1. 最小费用最大流精确求解（忽略启动次数限制）：
 *    源 -> 车辆（容量=需求）-> 窗口内时间格（容量=每格最大功率）-> 汇（容量=站点功率上限，费用=电价）。
 * 2. 若车辆启动次数超限，做会话合并修复（见 enforceMaxStarts 注释）。
 */
export function scheduleCharging(vehicles: VehicleInput[], station: StationProfile): ChargePlan {
  validateStation(station);
  for (const v of vehicles) validateVehicle(v, station.slotCount);

  const vehicleCount = vehicles.length;
  const slotCount = station.slotCount;
  const source = 0;
  const vehicleBase = 1;
  const slotBase = vehicleBase + vehicleCount;
  const sink = slotBase + slotCount;

  const mcf = new MinCostFlow(sink + 1);
  const totalDemand = vehicles.reduce((sum, v) => sum + v.demandEnergy, 0);

  const vehicleSlotEdges: { slot: number; edge: FlowEdge }[][] = [];
  for (let i = 0; i < vehicleCount; i++) {
    const v = vehicles[i];
    if (v.demandEnergy > 0) {
      mcf.addEdge(source, vehicleBase + i, v.demandEnergy, 0);
    }
    const edges: { slot: number; edge: FlowEdge }[] = [];
    if (v.demandEnergy > 0 && v.maxPowerPerSlot > 0) {
      for (let t = v.arrivalSlot; t < v.departureSlot; t++) {
        if (station.powerLimitPerSlot[t] > 0) {
          edges.push({
            slot: t,
            edge: mcf.addEdge(vehicleBase + i, slotBase + t, v.maxPowerPerSlot, 0),
          });
        }
      }
    }
    vehicleSlotEdges.push(edges);
  }
  for (let t = 0; t < slotCount; t++) {
    if (station.powerLimitPerSlot[t] > 0) {
      mcf.addEdge(slotBase + t, sink, station.powerLimitPerSlot[t], station.pricePerSlot[t]);
    }
  }

  mcf.minCostMaxFlow(source, sink, totalDemand);

  // 读取每车每格分配
  const allocations: Map<number, number>[] = vehicles.map(() => new Map());
  for (let i = 0; i < vehicleCount; i++) {
    for (const { slot, edge } of vehicleSlotEdges[i]) {
      const used = vehicles[i].maxPowerPerSlot - edge.cap;
      if (used > 0) allocations[i].set(slot, used);
    }
  }

  // 站点每格剩余容量（会话修复时原地维护）
  const residual = station.powerLimitPerSlot.slice();
  for (const alloc of allocations) {
    for (const [slot, energy] of alloc) residual[slot] -= energy;
  }

  // 启动次数限制修复；返回无法安置而舍弃的能量
  const startLimitLoss = new Array<number>(vehicleCount).fill(0);
  for (let i = 0; i < vehicleCount; i++) {
    const maxStarts = vehicles[i].maxStarts ?? Infinity;
    if (maxStarts === Infinity) continue;
    startLimitLoss[i] = enforceMaxStarts(
      allocations[i],
      vehicles[i],
      maxStarts,
      residual,
      station.pricePerSlot,
    );
  }

  // 汇总输出
  const schedules: VehicleSchedule[] = vehicles.map((v, i) => {
    const slots: SlotAllocation[] = [...allocations[i].entries()]
      .map(([slot, energy]) => ({ slot, energy }))
      .sort((a, b) => a.slot - b.slot);
    const sessions = extractRuns(allocations[i]).map((r) => ({
      startSlot: r.start,
      endSlot: r.end,
      energy: r.energy,
    }));
    const deliveredEnergy = slots.reduce((sum, a) => sum + a.energy, 0);
    const cost = slots.reduce((sum, a) => sum + a.energy * station.pricePerSlot[a.slot], 0);
    return { vehicleId: v.id, allocations: slots, sessions, deliveredEnergy, cost };
  });

  const unmetDemand: UnmetDemand[] = [];
  for (let i = 0; i < vehicleCount; i++) {
    const missing = vehicles[i].demandEnergy - schedules[i].deliveredEnergy;
    if (missing > 0) {
      unmetDemand.push({
        vehicleId: vehicles[i].id,
        requested: vehicles[i].demandEnergy,
        delivered: schedules[i].deliveredEnergy,
        missing,
      });
    }
  }

  const totalDelivered = schedules.reduce((sum, s) => sum + s.deliveredEnergy, 0);
  const feasible = totalDelivered === totalDemand;
  return {
    feasible,
    totalCost: schedules.reduce((sum, s) => sum + s.cost, 0),
    totalDemand,
    totalDelivered,
    schedules,
    unmetDemand,
    windowDeficits: feasible ? [] : computeWindowDeficits(vehicles, station, startLimitLoss),
  };
}

/** 把分配压缩为不超过 maxStarts 段连续会话。返回无法安置的能量（>0 表示限制下无解）。 */
function enforceMaxStarts(
  alloc: Map<number, number>,
  vehicle: VehicleInput,
  maxStarts: number,
  residual: number[],
  price: number[],
): number {
  let unplaced = 0;
  let runs = extractRuns(alloc);
  while (runs.length > maxStarts) {
    const candidates = [...runs].sort((a, b) => a.energy - b.energy);
    let repaired = false;
    for (const victim of candidates) {
      const remaining = runs.filter((r) => r !== victim);
      if (remaining.length === 0) continue;
      // 暂时移出 victim 的能量，再尝试迁入剩余会话跨度内/紧邻的空闲容量
      const removed: [number, number][] = [];
      for (let s = victim.start; s < victim.end; s++) {
        const e = alloc.get(s) ?? 0;
        if (e > 0) {
          alloc.delete(s);
          residual[s] += e;
          removed.push([s, e]);
        }
      }
      // 尝试把 victim 的能量迁入不会新增会话的时间格（费用增量最小）
      if (relocateEnergy(alloc, vehicle, residual, price, victim.energy) !== null) {
        repaired = true;
        break;
      }
      for (const [s, e] of removed) {
        alloc.set(s, e);
        residual[s] -= e;
      }
    }
    if (!repaired) {
      // 任何会话都无法整体迁移：舍弃能量最小的会话并记为缺口
      const victim = candidates[0];
      for (let s = victim.start; s < victim.end; s++) {
        const e = alloc.get(s) ?? 0;
        if (e > 0) {
          alloc.delete(s);
          residual[s] += e;
          unplaced += e;
        }
      }
    }
    runs = extractRuns(alloc);
  }
  return unplaced;
}

/**
 * 把 amount 单位的能量迁入不会新增会话的时间格：
 * 已有充电的格（补足至上限）或与现有充电段相邻的格（外延/桥接）。
 * 每步选择电价最低的可行格。成功返回移动记录，失败回滚并返回 null。
 */
function relocateEnergy(
  alloc: Map<number, number>,
  vehicle: VehicleInput,
  residual: number[],
  price: number[],
  amount: number,
): [number, number][] | null {
  const moves: [number, number][] = [];
  let need = amount;
  while (need > 0) {
    let best = -1;
    for (let s = vehicle.arrivalSlot; s < vehicle.departureSlot; s++) {
      const current = alloc.get(s) ?? 0;
      const active = current > 0;
      const frontier = !active && ((alloc.get(s - 1) ?? 0) > 0 || (alloc.get(s + 1) ?? 0) > 0);
      if (!active && !frontier) continue;
      const free = Math.min(vehicle.maxPowerPerSlot - current, residual[s]);
      if (free <= 0) continue;
      if (best < 0 || price[s] < price[best]) best = s;
    }
    if (best < 0) {
      for (const [slot, energy] of moves) {
        const left = (alloc.get(slot) ?? 0) - energy;
        if (left > 0) alloc.set(slot, left);
        else alloc.delete(slot);
        residual[slot] += energy;
      }
      return null;
    }
    const current = alloc.get(best) ?? 0;
    const add = Math.min(vehicle.maxPowerPerSlot - current, residual[best], need);
    alloc.set(best, current + add);
    residual[best] -= add;
    moves.push([best, add]);
    need -= add;
  }
  return moves;
}

/** 提取连续充电段（按时间升序）。 */
function extractRuns(alloc: Map<number, number>): Run[] {
  const slots = [...alloc.keys()].filter((s) => (alloc.get(s) ?? 0) > 0).sort((a, b) => a - b);
  const runs: Run[] = [];
  for (const s of slots) {
    const energy = alloc.get(s) as number;
    const last = runs[runs.length - 1];
    if (last && last.end === s) {
      last.end = s + 1;
      last.energy += energy;
    } else {
      runs.push({ start: s, end: s + 1, energy });
    }
  }
  return runs;
}

/**
 * 无解诊断：时间窗能量缺口。
 * 1) station-capacity：Horn 区间条件 —— 对每个区间 [s,e)，
 *    缺口 = 充电窗口完全落在区间内的车辆需求之和 - 区间站点功率容量。
 *    只报告不被更大区间支配的极大缺口。
 * 2) vehicle-power：车辆自身 最大功率 × 窗口长度 不足以覆盖需求。
 * 3) start-limit：启动次数限制导致无法安置的能量。
 */
function computeWindowDeficits(
  vehicles: VehicleInput[],
  station: StationProfile,
  startLimitLoss: number[],
): WindowDeficit[] {
  const T = station.slotCount;
  const deficits: WindowDeficit[] = [];

  if (T <= DEFICIT_GRID_SLOT_LIMIT) {
    const size = T + 1;
    // req(s,e) = 到达 >= s 且 离站 <= e 的车辆需求之和（2D 后缀/前缀和）
    const req = new Float64Array(size * size);
    for (const v of vehicles) {
      req[v.arrivalSlot * size + v.departureSlot] += v.demandEnergy;
    }
    for (let s = T - 1; s >= 0; s--) {
      for (let e = 0; e <= T; e++) req[s * size + e] += req[(s + 1) * size + e];
    }
    for (let s = 0; s <= T; s++) {
      for (let e = 1; e <= T; e++) req[s * size + e] += req[s * size + e - 1];
    }
    const capPrefix = new Float64Array(size);
    for (let t = 0; t < T; t++) capPrefix[t + 1] = capPrefix[t] + station.powerLimitPerSlot[t];

    // def(s,e) 仅对 s < e 有效；M(s,e) = 所有被 (s,e) 包含的区间的 def 最大值。
    // 只报告"极小"缺口区间：不存在缺口不小于它的真子区间（同等缺口下窗口越窄定位越准）。
    const def = new Float64Array(size * size).fill(Number.NEGATIVE_INFINITY);
    for (let s = 0; s < T; s++) {
      for (let e = s + 1; e <= T; e++) {
        def[s * size + e] = req[s * size + e] - (capPrefix[e] - capPrefix[s]);
      }
    }
    const containedMax = new Float64Array(size * size).fill(Number.NEGATIVE_INFINITY);
    for (let s = T - 1; s >= 0; s--) {
      for (let e = s + 1; e <= T; e++) {
        let m = def[s * size + e];
        m = Math.max(m, containedMax[(s + 1) * size + e]);
        if (e > s + 1) m = Math.max(m, containedMax[s * size + e - 1]);
        containedMax[s * size + e] = m;
      }
    }
    for (let s = 0; s < T; s++) {
      for (let e = s + 1; e <= T; e++) {
        const d = def[s * size + e];
        if (d <= 0) continue;
        const innerMax = Math.max(
          containedMax[(s + 1) * size + e],
          e > s + 1 ? containedMax[s * size + e - 1] : Number.NEGATIVE_INFINITY,
        );
        if (d > innerMax) {
          deficits.push({
            startSlot: s,
            endSlot: e,
            requiredEnergy: req[s * size + e],
            availableEnergy: capPrefix[e] - capPrefix[s],
            deficitEnergy: d,
            kind: "station-capacity",
          });
        }
      }
    }
  }

  for (let i = 0; i < vehicles.length; i++) {
    const v = vehicles[i];
    const windowLen = v.departureSlot - v.arrivalSlot;
    const powerCap = v.maxPowerPerSlot * windowLen;
    if (v.demandEnergy > powerCap) {
      deficits.push({
        startSlot: v.arrivalSlot,
        endSlot: v.departureSlot,
        requiredEnergy: v.demandEnergy,
        availableEnergy: powerCap,
        deficitEnergy: v.demandEnergy - powerCap,
        kind: "vehicle-power",
      });
    }
    if (startLimitLoss[i] > 0) {
      deficits.push({
        startSlot: v.arrivalSlot,
        endSlot: v.departureSlot,
        requiredEnergy: v.demandEnergy,
        availableEnergy: v.demandEnergy - startLimitLoss[i],
        deficitEnergy: startLimitLoss[i],
        kind: "start-limit",
      });
    }
  }

  deficits.sort((a, b) => a.startSlot - b.startSlot || a.endSlot - b.endSlot);
  return deficits;
}

function validateStation(station: StationProfile): void {
  if (!Number.isInteger(station.slotCount) || station.slotCount < 1) {
    throw new Error("station.slotCount 必须为正整数");
  }
  if (station.powerLimitPerSlot.length !== station.slotCount) {
    throw new Error("powerLimitPerSlot 长度必须等于 slotCount");
  }
  if (station.pricePerSlot.length !== station.slotCount) {
    throw new Error("pricePerSlot 长度必须等于 slotCount");
  }
  for (let t = 0; t < station.slotCount; t++) {
    if (!Number.isInteger(station.powerLimitPerSlot[t]) || station.powerLimitPerSlot[t] < 0) {
      throw new Error(`powerLimitPerSlot[${t}] 必须为非负整数`);
    }
    if (!Number.isInteger(station.pricePerSlot[t]) || station.pricePerSlot[t] < 0) {
      throw new Error(`pricePerSlot[${t}] 必须为非负整数`);
    }
  }
}

function validateVehicle(v: VehicleInput, slotCount: number): void {
  if (typeof v.id !== "string" || v.id.length === 0) {
    throw new Error("车辆 id 必须为非空字符串");
  }
  if (!Number.isInteger(v.arrivalSlot) || !Number.isInteger(v.departureSlot)) {
    throw new Error(`车辆 ${v.id} 的到离站时间必须为整数时间格`);
  }
  if (v.arrivalSlot < 0 || v.departureSlot > slotCount || v.arrivalSlot >= v.departureSlot) {
    throw new Error(`车辆 ${v.id} 的充电窗口 [${v.arrivalSlot}, ${v.departureSlot}) 越界或为空`);
  }
  if (!Number.isInteger(v.demandEnergy) || v.demandEnergy < 0) {
    throw new Error(`车辆 ${v.id} 的 demandEnergy 必须为非负整数`);
  }
  if (!Number.isInteger(v.maxPowerPerSlot) || v.maxPowerPerSlot < 0) {
    throw new Error(`车辆 ${v.id} 的 maxPowerPerSlot 必须为非负整数`);
  }
  if (v.maxStarts !== undefined && (!Number.isInteger(v.maxStarts) || v.maxStarts < 1)) {
    throw new Error(`车辆 ${v.id} 的 maxStarts 必须为 >= 1 的整数`);
  }
}
