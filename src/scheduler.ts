/**
 * 充电调度主入口。
 *
 * 求解流程：
 * 1. 最小费用最大流求出忽略启动次数限制时的最低费用计划（此时即为全局最优）。
 * 2. 若存在启动次数限制且流解违反限制，进入修复循环：
 *    逐个把超限车辆从站点占用中移除，用单车辆精确 DP 在剩余容量上重排
 *    （坐标下降，每辆车的子问题是精确的；单车辆场景整体即为精确最优）。
 * 3. 若不可行，输出能量缺口报告：
 *    - 总体缺口：联合最大流缺口与各车独立缺口之和的较大者（真实缺口的下界）；
 *    - 单车缺口：该车独占站点时（含启动次数限制）的最大可充能量与需求之差；
 *    - 时间窗缺口：按离站截止时间做前缀最大流，给出每个时间窗的能量缺口。
 */
import { buildFlowNetwork, deliveredPerVehicle, extractAllocations } from './flowScheduler';
import type { FlowNetwork } from './flowScheduler';
import { scheduleVehicleDp } from './vehicleDp';
import { validateProblem } from './validate';
import type {
  ChargingProblem,
  FeasibleResult,
  InfeasibleResult,
  VehicleDeficit,
  VehiclePlan,
  WindowDeficit,
} from './types';

type Allocation = Map<number, number>;

/** 统计充电启动次数：窗口内从“不充”到“充”的跳变次数。 */
export function countStarts(alloc: Allocation, arrival: number, departure: number): number {
  let starts = 0;
  let active = false;
  for (let t = arrival; t < departure; t++) {
    const charging = (alloc.get(t) ?? 0) > 0;
    if (charging && !active) starts++;
    active = charging;
  }
  return starts;
}

export function scheduleCharging(problem: ChargingProblem): FeasibleResult | InfeasibleResult {
  validateProblem(problem);
  const { vehicles } = problem;
  const totalDemand = vehicles.reduce((sum, v) => sum + v.demand, 0);

  const network = buildFlowNetwork(problem);
  const solution = network.mcf.solve(network.source, network.sink);

  const allocs: Allocation[] = vehicles.map(() => new Map());
  const extracted = extractAllocations(network);
  network.vehicleIndices.forEach((vehicleIndex, local) => {
    allocs[vehicleIndex] = extracted[local];
  });

  if (solution.flow < totalDemand) {
    return buildInfeasibleResult(problem, network, solution.flow, totalDemand);
  }

  const hasStartLimit = vehicles.some((v) => v.maxStarts !== undefined);
  if (hasStartLimit && !repairStartLimits(problem, allocs)) {
    return buildInfeasibleResult(problem, network, solution.flow, totalDemand);
  }

  return buildFeasibleResult(problem, allocs);
}

function buildFeasibleResult(problem: ChargingProblem, allocs: Allocation[]): FeasibleResult {
  const plan: VehiclePlan[] = problem.vehicles.map((v, i) => {
    const entries = [...allocs[i].entries()].sort((a, b) => a[0] - b[0]);
    let energy = 0;
    let cost = 0;
    for (const [slot, x] of entries) {
      energy += x;
      cost += x * problem.slots[slot].price;
    }
    return {
      vehicleId: v.id,
      energy,
      cost,
      starts: countStarts(allocs[i], v.arrival, v.departure),
      allocations: entries.map(([slot, energy]) => ({ slot, energy })),
    };
  });
  return {
    feasible: true,
    totalCost: plan.reduce((sum, p) => sum + p.cost, 0),
    totalEnergy: plan.reduce((sum, p) => sum + p.energy, 0),
    plan,
  };
}

/**
 * 启动次数修复循环。直接在 allocs 上原地修改；返回是否得到全部满足限制的计划。
 */
function repairStartLimits(problem: ChargingProblem, allocs: Allocation[]): boolean {
  const { slots, vehicles } = problem;
  const maxIterations = problem.options?.maxRepairIterations ?? 16;
  const prices = slots.map((s) => s.price);

  const usage = new Array<number>(slots.length).fill(0);
  allocs.forEach((alloc) => {
    for (const [t, x] of alloc) usage[t] += x;
  });

  const limitedIndices: number[] = [];
  vehicles.forEach((v, i) => {
    if (v.maxStarts !== undefined) limitedIndices.push(i);
  });

  const satisfied = (): boolean =>
    limitedIndices.every((i) => {
      const v = vehicles[i];
      return countStarts(allocs[i], v.arrival, v.departure) <= (v.maxStarts as number);
    });

  for (let iteration = 0; iteration < maxIterations; iteration++) {
    if (satisfied()) return true;
    let changed = false;
    for (const i of limitedIndices) {
      const v = vehicles[i];
      const maxStarts = v.maxStarts as number;
      if (countStarts(allocs[i], v.arrival, v.departure) <= maxStarts) continue;

      for (const [t, x] of allocs[i]) usage[t] -= x;
      const residual = slots.map((s, t) => s.powerLimit - usage[t]);
      const dp = scheduleVehicleDp({
        caps: residual,
        prices,
        arrival: v.arrival,
        departure: v.departure,
        demand: v.demand,
        maxPower: v.maxPower,
        maxStarts,
      });
      if (dp.feasible) {
        allocs[i] = dp.allocation;
        for (const [t, x] of dp.allocation) usage[t] += x;
        changed = true;
      } else {
        // 暂时无法修复：恢复旧分配，等其他车辆释放容量后再试。
        for (const [t, x] of allocs[i]) usage[t] += x;
      }
    }
    if (!changed) return satisfied();
  }
  return satisfied();
}

function buildInfeasibleResult(
  problem: ChargingProblem,
  network: FlowNetwork,
  jointFlow: number,
  totalDemand: number,
): InfeasibleResult {
  const { slots, vehicles } = problem;
  const flowDelivered = deliveredPerVehicle(network);
  const perVehicleFlow = new Array<number>(vehicles.length).fill(0);
  network.vehicleIndices.forEach((vehicleIndex, local) => {
    perVehicleFlow[vehicleIndex] = flowDelivered[local];
  });

  const caps = slots.map((s) => s.powerLimit);
  const prices = slots.map((s) => s.price);

  const vehicleDeficits: VehicleDeficit[] = vehicles.map((v, i) => {
    // 单车独占站点时的最大可充能量。
    let deliverable: number;
    if (v.maxStarts === undefined) {
      let sum = 0;
      for (let t = v.arrival; t < v.departure; t++) {
        sum += Math.min(v.maxPower, caps[t]);
      }
      deliverable = Math.min(v.demand, sum);
    } else {
      const dp = scheduleVehicleDp({
        caps,
        prices,
        arrival: v.arrival,
        departure: v.departure,
        demand: v.demand,
        maxPower: v.maxPower,
        maxStarts: v.maxStarts,
      });
      deliverable = dp.delivered;
    }
    // 也不能超过联合最大流实际分给它的上界信息之外，这里 deliverable 是独立上界。
    return {
      vehicleId: v.id,
      windowStart: v.arrival,
      windowEnd: v.departure,
      required: v.demand,
      deliverable,
      deficit: v.demand - deliverable,
    };
  });

  const windowDeficits = computeWindowDeficits(problem);

  const flowDeficit = totalDemand - jointFlow;
  const standaloneDeficit = vehicleDeficits.reduce((sum, d) => sum + Math.max(0, d.deficit), 0);
  const totalDeficit = Math.max(flowDeficit, standaloneDeficit);

  return {
    feasible: false,
    totalDemand,
    totalDeliverable: totalDemand - totalDeficit,
    totalDeficit,
    vehicleDeficits,
    windowDeficits,
  };
}

/**
 * 时间窗能量缺口：对每个离站截止时间 d（对应时隙 d-1），
 * 统计 departure <= d 的车辆需求之和，与它们在时隙 [0, d-1] 内的最大可交付能量（前缀最大流）之差。
 */
function computeWindowDeficits(problem: ChargingProblem): WindowDeficit[] {
  const deadlines = [...new Set(problem.vehicles.map((v) => v.departure))].sort((a, b) => a - b);
  const deficits: WindowDeficit[] = [];
  for (const deadline of deadlines) {
    const required = problem.vehicles
      .filter((v) => v.departure <= deadline)
      .reduce((sum, v) => sum + v.demand, 0);
    if (required === 0) continue;
    const network = buildFlowNetwork(problem, deadline - 1);
    const { flow } = network.mcf.solve(network.source, network.sink);
    if (flow < required) {
      deficits.push({
        untilSlot: deadline - 1,
        requiredByThen: required,
        deliverableByThen: flow,
        deficit: required - flow,
      });
    }
  }
  return deficits;
}
