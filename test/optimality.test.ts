import test from "node:test";
import assert from "node:assert/strict";
import { scheduleCharging } from "../src/index.ts";
import type { StationProfile, VehicleInput } from "../src/index.ts";

/** 暴力枚举全部整数分配，返回满足需求的最小费用；不可行返回 null。 */
function bruteForceMinCost(vehicles: VehicleInput[], st: StationProfile): number | null {
  const used = new Array<number>(st.slotCount).fill(0);
  let best: number | null = null;
  const current = vehicles.map(() => new Map<number, number>());

  function dfs(i: number, cost: number): void {
    if (best !== null && cost >= best) return;
    if (i === vehicles.length) {
      best = cost;
      return;
    }
    const v = vehicles[i];
    const window: number[] = [];
    for (let t = v.arrivalSlot; t < v.departureSlot; t++) window.push(t);
    const assign = new Array<number>(window.length).fill(0);

    function gen(j: number, remaining: number): void {
      if (best !== null && cost + slotCost(i) >= best) return;
      if (j === window.length) {
        if (remaining === 0) {
          for (let k = 0; k < window.length; k++) used[window[k]] += assign[k];
          dfs(i + 1, cost + slotCost(i));
          for (let k = 0; k < window.length; k++) used[window[k]] -= assign[k];
        }
        return;
      }
      const t = window[j];
      const cap = Math.min(v.maxPowerPerSlot, st.powerLimitPerSlot[t] - used[t], remaining);
      for (let e = 0; e <= cap; e++) {
        assign[j] = e;
        gen(j + 1, remaining - e);
      }
      assign[j] = 0;
    }

    function slotCost(vehicleIdx: number): number {
      let c = 0;
      const assignArr = vehicleIdx === i ? assign : null;
      if (assignArr) {
        for (let k = 0; k < window.length; k++) c += assignArr[k] * st.pricePerSlot[window[k]];
      } else {
        for (const [slot, e] of current[vehicleIdx]) c += e * st.pricePerSlot[slot];
      }
      return c;
    }

    gen(0, v.demandEnergy);
  }

  dfs(0, 0);
  return best;
}

test("小规模随机算例与暴力枚举结果一致", () => {
  let seed = 20240601;
  const rand = (n: number): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % n;
  };
  for (let iter = 0; iter < 300; iter++) {
    const T = 2 + rand(3);
    const st: StationProfile = {
      slotCount: T,
      powerLimitPerSlot: Array.from({ length: T }, () => rand(4)),
      pricePerSlot: Array.from({ length: T }, () => rand(5)),
    };
    const vehicles: VehicleInput[] = [];
    const n = 1 + rand(3);
    for (let i = 0; i < n; i++) {
      const arrival = rand(T);
      const departure = arrival + 1 + rand(T - arrival);
      vehicles.push({
        id: `V${i}`,
        arrivalSlot: arrival,
        departureSlot: departure,
        demandEnergy: rand(5),
        maxPowerPerSlot: rand(4),
      });
    }
    const plan = scheduleCharging(vehicles, st);
    const brute = bruteForceMinCost(vehicles, st);
    if (brute === null) {
      assert.equal(plan.feasible, false, `iter ${iter}: 应为无解`);
    } else {
      assert.equal(plan.feasible, true, `iter ${iter}: 应有解`);
      assert.equal(plan.totalCost, brute, `iter ${iter}: 费用应最优`);
    }
  }
});
