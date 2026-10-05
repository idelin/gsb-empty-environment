/**
 * 单车辆精确动态规划：在给定的分时剩余容量、分时电价下，
 * 求满足需求且充电启动次数不超过 maxStarts 的最低费用充电计划。
 *
 * 状态：dp[t](已充能量 e, 已用启动次数 k, 上一时隙是否在充电) = 最低费用。
 * 转移：在当前时隙选择充电量 x ∈ [0, min(剩余容量, maxPower, 需求-已充)]，
 *       若 x > 0 且上一时隙未充电，则消耗一次启动。
 *
 * 复杂度 O(窗口时隙数 × 需求 × 启动上限 × 单时隙上限)，所有量均为整数。
 * 当需求不可满足时，返回在启动次数限制下可交付的最大能量及其最低费用。
 */

export interface VehicleDpInput {
  /** 每个时隙可用的剩余站点容量（长度 = 时隙总数）。 */
  caps: number[];
  /** 每个时隙的单位电价（长度 = 时隙总数）。 */
  prices: number[];
  arrival: number;
  /** 不含。 */
  departure: number;
  demand: number;
  maxPower: number;
  maxStarts: number;
}

export interface VehicleDpResult {
  /** 是否恰好满足需求。 */
  feasible: boolean;
  /** 实际可交付能量（feasible 时等于 demand，否则为限制下的最大值）。 */
  delivered: number;
  /** 交付 delivered 能量的最低费用。 */
  cost: number;
  /** 充电启动次数。 */
  starts: number;
  /** 时隙 -> 充电量（仅非零项）。 */
  allocation: Map<number, number>;
}

export function scheduleVehicleDp(input: VehicleDpInput): VehicleDpResult {
  const { caps, prices, arrival, departure, demand, maxPower } = input;
  const windowLength = departure - arrival;
  const maxStarts = Math.min(input.maxStarts, windowLength);

  const empty = new Map<number, number>();
  if (demand === 0) {
    return { feasible: true, delivered: 0, cost: 0, starts: 0, allocation: empty };
  }
  if (maxStarts === 0 || maxPower === 0) {
    return { feasible: false, delivered: 0, cost: 0, starts: 0, allocation: empty };
  }

  const stateCount = (demand + 1) * (maxStarts + 1) * 2;
  const keyOf = (energy: number, starts: number, prevActive: number): number =>
    (energy * (maxStarts + 1) + starts) * 2 + prevActive;

  let current = new Float64Array(stateCount).fill(Infinity);
  const parentKeys: Int32Array[] = [];
  const takenAmounts: Int32Array[] = [];
  current[keyOf(0, 0, 0)] = 0;

  for (let t = arrival; t < departure; t++) {
    const next = new Float64Array(stateCount).fill(Infinity);
    const parentKey = new Int32Array(stateCount).fill(-1);
    const taken = new Int32Array(stateCount);
    const slotCap = Math.max(0, Math.min(caps[t], maxPower));
    const price = prices[t];

    for (let energy = 0; energy <= demand; energy++) {
      const xMax = Math.min(slotCap, demand - energy);
      for (let starts = 0; starts <= maxStarts; starts++) {
        for (let prevActive = 0; prevActive <= 1; prevActive++) {
          const costHere = current[keyOf(energy, starts, prevActive)];
          if (costHere === Infinity) continue;
          for (let x = 0; x <= xMax; x++) {
            const nextStarts = x > 0 && prevActive === 0 ? starts + 1 : starts;
            if (nextStarts > maxStarts) continue;
            const nextActive = x > 0 ? 1 : 0;
            const nextCost = costHere + x * price;
            const nextKey = keyOf(energy + x, nextStarts, nextActive);
            if (nextCost < next[nextKey]) {
              next[nextKey] = nextCost;
              parentKey[nextKey] = keyOf(energy, starts, prevActive);
              taken[nextKey] = x;
            }
          }
        }
      }
    }
    current = next;
    parentKeys.push(parentKey);
    takenAmounts.push(taken);
  }

  // 目标：先最大化交付能量，再最小化费用。
  let delivered = 0;
  let bestCost = Infinity;
  let bestState = -1;
  for (let energy = demand; energy >= 0; energy--) {
    for (let starts = 0; starts <= maxStarts; starts++) {
      for (let prevActive = 0; prevActive <= 1; prevActive++) {
        const key = keyOf(energy, starts, prevActive);
        if (current[key] < bestCost) {
          bestCost = current[key];
          bestState = key;
        }
      }
    }
    if (bestState >= 0) {
      delivered = energy;
      break;
    }
  }

  const allocation = new Map<number, number>();
  let state = bestState;
  for (let i = windowLength - 1; i >= 0; i--) {
    const x = takenAmounts[i][state];
    if (x > 0) {
      allocation.set(arrival + i, x);
    }
    state = parentKeys[i][state];
  }

  let starts = 0;
  let active = false;
  for (let t = arrival; t < departure; t++) {
    const x = allocation.get(t) ?? 0;
    if (x > 0 && !active) starts++;
    active = x > 0;
  }

  return {
    feasible: delivered === demand,
    delivered,
    cost: bestCost,
    starts,
    allocation,
  };
}
