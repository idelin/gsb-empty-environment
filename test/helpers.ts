import type { ChargingProblem, FeasibleResult } from '../src/index';

/** 可复现的伪随机数发生器。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * 暴力枚举求最低费用（不考虑启动次数限制），不可行返回 null。
 * 仅用于小规模对拍。
 */
export function bruteForceMinCost(problem: ChargingProblem): number | null {
  const { slots, vehicles } = problem;
  const usage = new Array<number>(slots.length).fill(0);
  let best = Infinity;

  const assignVehicle = (vi: number, cost: number): void => {
    if (cost >= best) return;
    if (vi === vehicles.length) {
      best = cost;
      return;
    }
    const v = vehicles[vi];
    const windowSlots: number[] = [];
    for (let t = v.arrival; t < v.departure; t++) windowSlots.push(t);

    const assignSlot = (si: number, delivered: number, c: number): void => {
      if (delivered === v.demand) {
        assignVehicle(vi + 1, c);
        return;
      }
      if (si === windowSlots.length) return;
      // 剪枝：剩余时隙全部填满也不够就放弃。
      let remaining = 0;
      for (let j = si; j < windowSlots.length; j++) {
        remaining += Math.min(v.maxPower, slots[windowSlots[j]].powerLimit - usage[windowSlots[j]]);
      }
      if (delivered + remaining < v.demand) return;

      const t = windowSlots[si];
      const xMax = Math.min(v.maxPower, v.demand - delivered, slots[t].powerLimit - usage[t]);
      for (let x = 0; x <= xMax; x++) {
        usage[t] += x;
        assignSlot(si + 1, delivered + x, c + x * slots[t].price);
        usage[t] -= x;
      }
    };
    assignSlot(0, 0, cost);
  };
  assignVehicle(0, 0);
  return best === Infinity ? null : best;
}

/**
 * 暴力枚举单车辆在启动次数限制下的最低费用，不可行返回 null。
 */
export function bruteForceSingleVehicleStarts(problem: ChargingProblem): number | null {
  const { slots, vehicles } = problem;
  if (vehicles.length !== 1) throw new Error('expected exactly one vehicle');
  const v = vehicles[0];
  const maxStarts = v.maxStarts;
  if (maxStarts === undefined) throw new Error('expected maxStarts to be set');
  let best = Infinity;

  const assign = (t: number, delivered: number, starts: number, prevActive: boolean, cost: number): void => {
    if (cost >= best) return;
    if (delivered === v.demand) {
      best = cost;
      return;
    }
    if (t === v.departure) return;
    const xMax = Math.min(v.maxPower, v.demand - delivered, slots[t].powerLimit);
    for (let x = 0; x <= xMax; x++) {
      const nextStarts = x > 0 && !prevActive ? starts + 1 : starts;
      if (nextStarts > maxStarts) continue;
      assign(t + 1, delivered + x, nextStarts, x > 0, cost + x * slots[t].price);
    }
  };
  assign(v.arrival, 0, 0, false, 0);
  return best === Infinity ? null : best;
}

/** 校验可行解的所有约束。返回错误信息列表（空数组表示合法）。 */
export function checkPlanConsistency(problem: ChargingProblem, result: FeasibleResult): string[] {
  const errors: string[] = [];
  const { slots, vehicles } = problem;
  const usage = new Array<number>(slots.length).fill(0);
  let totalCost = 0;
  let totalEnergy = 0;

  if (result.plan.length !== vehicles.length) {
    errors.push(`plan length ${result.plan.length} != vehicles length ${vehicles.length}`);
    return errors;
  }

  vehicles.forEach((v, i) => {
    const p = result.plan[i];
    if (p.vehicleId !== v.id) errors.push(`plan[${i}] id ${p.vehicleId} != ${v.id}`);
    let energy = 0;
    let cost = 0;
    let prevSlot = -2;
    let starts = 0;
    for (const a of p.allocations) {
      if (a.energy <= 0) errors.push(`${v.id}: non-positive allocation at slot ${a.slot}`);
      if (!Number.isInteger(a.energy)) errors.push(`${v.id}: non-integer energy at slot ${a.slot}`);
      if (a.slot < v.arrival || a.slot >= v.departure) {
        errors.push(`${v.id}: allocation at slot ${a.slot} outside window [${v.arrival}, ${v.departure})`);
      }
      if (a.energy > v.maxPower) errors.push(`${v.id}: energy ${a.energy} > maxPower ${v.maxPower} at slot ${a.slot}`);
      if (a.slot !== prevSlot + 1) starts++;
      prevSlot = a.slot;
      energy += a.energy;
      cost += a.energy * slots[a.slot].price;
      usage[a.slot] += a.energy;
    }
    if (energy !== v.demand) errors.push(`${v.id}: energy ${energy} != demand ${v.demand}`);
    if (energy !== p.energy) errors.push(`${v.id}: reported energy ${p.energy} != actual ${energy}`);
    if (cost !== p.cost) errors.push(`${v.id}: reported cost ${p.cost} != actual ${cost}`);
    if (starts !== p.starts) errors.push(`${v.id}: reported starts ${p.starts} != actual ${starts}`);
    if (v.maxStarts !== undefined && starts > v.maxStarts) {
      errors.push(`${v.id}: starts ${starts} > maxStarts ${v.maxStarts}`);
    }
    totalCost += cost;
    totalEnergy += energy;
  });

  usage.forEach((u, t) => {
    if (u > slots[t].powerLimit) errors.push(`slot ${t}: usage ${u} > powerLimit ${slots[t].powerLimit}`);
  });
  if (totalCost !== result.totalCost) errors.push(`totalCost ${result.totalCost} != actual ${totalCost}`);
  if (totalEnergy !== result.totalEnergy) errors.push(`totalEnergy ${result.totalEnergy} != actual ${totalEnergy}`);
  return errors;
}
