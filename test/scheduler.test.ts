import { describe, expect, test } from 'bun:test';
import {
  ChargingValidationError,
  scheduleCharging,
  scheduleVehicleDp,
} from '../src/index';
import type { ChargingProblem, Vehicle } from '../src/index';
import {
  bruteForceMinCost,
  bruteForceSingleVehicleStarts,
  checkPlanConsistency,
  mulberry32,
} from './helpers';

describe('basic scheduling', () => {
  test('charges at cheapest slots within the window', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 10, price: 3 },
        { powerLimit: 10, price: 1 },
        { powerLimit: 10, price: 2 },
        { powerLimit: 10, price: 1 },
        { powerLimit: 10, price: 2 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 5, demand: 4, maxPower: 2 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.totalCost).toBe(4); // 2 @ slot1 + 2 @ slot3, price 1 each
    expect(result.plan[0].energy).toBe(4);
    expect(result.plan[0].allocations).toEqual([
      { slot: 1, energy: 2 },
      { slot: 3, energy: 2 },
    ]);
  });

  test('respects per-vehicle maxPower', () => {
    const problem: ChargingProblem = {
      slots: Array.from({ length: 4 }, () => ({ powerLimit: 10, price: 1 })),
      vehicles: [{ id: 'a', arrival: 0, departure: 4, demand: 4, maxPower: 1 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.plan[0].allocations).toEqual([
      { slot: 0, energy: 1 },
      { slot: 1, energy: 1 },
      { slot: 2, energy: 1 },
      { slot: 3, energy: 1 },
    ]);
  });

  test('departure slot is exclusive', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 5, price: 5 },
        { powerLimit: 5, price: 1 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 1, demand: 1, maxPower: 5 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.totalCost).toBe(5); // 不能用便宜的时隙 1
    expect(result.plan[0].allocations).toEqual([{ slot: 0, energy: 1 }]);
  });

  test('arrival slot is inclusive', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 5, price: 1 },
        { powerLimit: 5, price: 9 },
      ],
      vehicles: [{ id: 'a', arrival: 1, departure: 2, demand: 2, maxPower: 5 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.totalCost).toBe(18);
  });

  test('shares station power between vehicles at min cost', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 1, price: 1 },
        { powerLimit: 1, price: 2 },
      ],
      vehicles: [
        { id: 'a', arrival: 0, departure: 2, demand: 1, maxPower: 1 },
        { id: 'b', arrival: 0, departure: 2, demand: 1, maxPower: 1 },
      ],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.totalCost).toBe(3); // 一辆在时隙0，另一辆在时隙1
    expect(checkPlanConsistency(problem, result)).toEqual([]);
  });

  test('handles zero-demand vehicles', () => {
    const problem: ChargingProblem = {
      slots: [{ powerLimit: 1, price: 1 }],
      vehicles: [{ id: 'a', arrival: 0, departure: 1, demand: 0, maxPower: 1, maxStarts: 0 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.plan[0].energy).toBe(0);
    expect(result.plan[0].starts).toBe(0);
    expect(result.plan[0].allocations).toEqual([]);
  });

  test('supports negative prices', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 2, price: -2 },
        { powerLimit: 2, price: 1 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 2, demand: 1, maxPower: 2 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.totalCost).toBe(-2);
    expect(result.plan[0].allocations).toEqual([{ slot: 0, energy: 1 }]);
  });
});

describe('infeasible reporting', () => {
  test('reports vehicle and window deficits when demand exceeds capacity', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 2, price: 1 },
        { powerLimit: 2, price: 1 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 2, demand: 10, maxPower: 3 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(false);
    if (result.feasible) return;
    expect(result.totalDemand).toBe(10);
    expect(result.totalDeficit).toBe(6);
    expect(result.totalDeliverable).toBe(4);
    expect(result.vehicleDeficits).toEqual([
      { vehicleId: 'a', windowStart: 0, windowEnd: 2, required: 10, deliverable: 4, deficit: 6 },
    ]);
    expect(result.windowDeficits).toEqual([
      { untilSlot: 1, requiredByThen: 10, deliverableByThen: 4, deficit: 6 },
    ]);
  });

  test('reports deficits per deadline window', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 1, price: 1 },
        { powerLimit: 1, price: 1 },
        { powerLimit: 1, price: 1 },
      ],
      vehicles: [
        { id: 'a', arrival: 0, departure: 1, demand: 2, maxPower: 5 },
        { id: 'b', arrival: 0, departure: 3, demand: 3, maxPower: 5 },
      ],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(false);
    if (result.feasible) return;
    expect(result.totalDeficit).toBe(2); // 需求 5，容量 3
    expect(result.windowDeficits).toEqual([
      { untilSlot: 0, requiredByThen: 2, deliverableByThen: 1, deficit: 1 },
      { untilSlot: 2, requiredByThen: 5, deliverableByThen: 3, deficit: 2 },
    ]);
    const deficitA = result.vehicleDeficits.find((d) => d.vehicleId === 'a');
    const deficitB = result.vehicleDeficits.find((d) => d.vehicleId === 'b');
    expect(deficitA).toMatchObject({ required: 2, deliverable: 1, deficit: 1 });
    expect(deficitB).toMatchObject({ required: 3, deliverable: 3, deficit: 0 });
  });

  test('maxPower smaller than station capacity limits deliverable energy', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 10, price: 1 },
        { powerLimit: 10, price: 1 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 2, demand: 5, maxPower: 2 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(false);
    if (result.feasible) return;
    expect(result.vehicleDeficits[0]).toMatchObject({ deliverable: 4, deficit: 1 });
  });
});

describe('charge start limits', () => {
  test('enforces a single contiguous run when maxStarts = 1', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 1, price: 1 },
        { powerLimit: 1, price: 5 },
        { powerLimit: 1, price: 1 },
        { powerLimit: 1, price: 5 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 4, demand: 2, maxPower: 1, maxStarts: 1 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    // 无限制时会选时隙 0 和 2（费用 2，启动 2 次）；限制 1 次启动只能连续充，费用 6。
    expect(result.plan[0].starts).toBe(1);
    expect(result.totalCost).toBe(6);
    expect(checkPlanConsistency(problem, result)).toEqual([]);
  });

  test('reports deficit when start limit makes demand unreachable', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 1, price: 1 },
        { powerLimit: 0, price: 1 },
        { powerLimit: 1, price: 1 },
      ],
      vehicles: [{ id: 'a', arrival: 0, departure: 3, demand: 2, maxPower: 1, maxStarts: 1 }],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(false);
    if (result.feasible) return;
    // 容量被零功率时隙断开，单次启动最多充 1。
    expect(result.vehicleDeficits[0]).toMatchObject({ required: 2, deliverable: 1, deficit: 1 });
    expect(result.totalDeficit).toBe(1);
  });

  test('repairs multiple vehicles with start limits against shared capacity', () => {
    const problem: ChargingProblem = {
      slots: [
        { powerLimit: 2, price: 1 },
        { powerLimit: 2, price: 2 },
        { powerLimit: 2, price: 1 },
        { powerLimit: 2, price: 2 },
      ],
      vehicles: [
        { id: 'a', arrival: 0, departure: 4, demand: 2, maxPower: 1, maxStarts: 1 },
        { id: 'b', arrival: 0, departure: 4, demand: 2, maxPower: 1, maxStarts: 1 },
      ],
    };
    const result = scheduleCharging(problem);
    expect(result.feasible).toBe(true);
    if (!result.feasible) return;
    expect(result.plan[0].starts).toBe(1);
    expect(result.plan[1].starts).toBe(1);
    expect(result.totalCost).toBe(6); // 每辆车最便宜的连续两段都是 1+2=3
    expect(checkPlanConsistency(problem, result)).toEqual([]);
  });

  test('vehicle DP matches expectations on a gap-split window', () => {
    const dp = scheduleVehicleDp({
      caps: [2, 0, 2],
      prices: [1, 1, 1],
      arrival: 0,
      departure: 3,
      demand: 3,
      maxPower: 2,
      maxStarts: 2,
    });
    expect(dp.feasible).toBe(true);
    expect(dp.delivered).toBe(3);
    expect(dp.starts).toBe(2);
    expect(dp.cost).toBe(3);
  });
});

describe('validation', () => {
  const base: ChargingProblem = {
    slots: [{ powerLimit: 1, price: 1 }],
    vehicles: [{ id: 'a', arrival: 0, departure: 1, demand: 1, maxPower: 1 }],
  };

  test('rejects non-integer values', () => {
    const problem: ChargingProblem = {
      ...base,
      vehicles: [{ id: 'a', arrival: 0, departure: 1, demand: 1.5, maxPower: 1 }],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });

  test('rejects duplicate vehicle ids', () => {
    const problem: ChargingProblem = {
      slots: [{ powerLimit: 2, price: 1 }],
      vehicles: [
        { id: 'a', arrival: 0, departure: 1, demand: 1, maxPower: 1 },
        { id: 'a', arrival: 0, departure: 1, demand: 1, maxPower: 1 },
      ],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });

  test('rejects departure outside slot range', () => {
    const problem: ChargingProblem = {
      ...base,
      vehicles: [{ id: 'a', arrival: 0, departure: 2, demand: 1, maxPower: 1 }],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });

  test('rejects arrival >= departure', () => {
    const problem: ChargingProblem = {
      ...base,
      vehicles: [{ id: 'a', arrival: 1, departure: 1, demand: 1, maxPower: 1 }],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });

  test('rejects negative power limit', () => {
    const problem: ChargingProblem = {
      slots: [{ powerLimit: -1, price: 1 }],
      vehicles: [],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });

  test('rejects negative maxStarts', () => {
    const problem: ChargingProblem = {
      ...base,
      vehicles: [{ id: 'a', arrival: 0, departure: 1, demand: 1, maxPower: 1, maxStarts: -1 }],
    };
    expect(() => scheduleCharging(problem)).toThrow(ChargingValidationError);
  });
});

describe('randomized cross-check against brute force', () => {
  function randomProblem(rng: () => number, withStartLimits: boolean): ChargingProblem {
    const slotCount = 1 + Math.floor(rng() * 5);
    const slots = Array.from({ length: slotCount }, () => ({
      powerLimit: Math.floor(rng() * 4),
      price: Math.floor(rng() * 5),
    }));
    const vehicleCount = withStartLimits ? 1 : 1 + Math.floor(rng() * 3);
    const vehicles: Vehicle[] = [];
    for (let i = 0; i < vehicleCount; i++) {
      const arrival = Math.floor(rng() * slotCount);
      const departure = arrival + 1 + Math.floor(rng() * (slotCount - arrival));
      const v: Vehicle = {
        id: `v${i}`,
        arrival,
        departure,
        demand: Math.floor(rng() * 5),
        maxPower: Math.floor(rng() * 4),
      };
      if (withStartLimits) {
        v.maxStarts = 1 + Math.floor(rng() * 3);
      }
      vehicles.push(v);
    }
    return { slots, vehicles };
  }

  test('min-cost flow matches brute force on 300 random instances', () => {
    const rng = mulberry32(42);
    for (let iter = 0; iter < 300; iter++) {
      const problem = randomProblem(rng, false);
      const result = scheduleCharging(problem);
      const brute = bruteForceMinCost(problem);
      expect(result.feasible).toBe(brute !== null);
      if (result.feasible && brute !== null) {
        expect(checkPlanConsistency(problem, result)).toEqual([]);
        expect(result.totalCost).toBe(brute);
      }
    }
  });

  test('start-limited single vehicle matches brute force on 300 random instances', () => {
    const rng = mulberry32(7);
    for (let iter = 0; iter < 300; iter++) {
      const problem = randomProblem(rng, true);
      const result = scheduleCharging(problem);
      const brute = bruteForceSingleVehicleStarts(problem);
      expect(result.feasible).toBe(brute !== null);
      if (result.feasible && brute !== null) {
        expect(checkPlanConsistency(problem, result)).toEqual([]);
        expect(result.totalCost).toBe(brute);
      }
    }
  });
});
