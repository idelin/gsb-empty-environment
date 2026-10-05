import test from "node:test";
import assert from "node:assert/strict";
import { scheduleCharging } from "../src/index.ts";
import type { ChargePlan, StationProfile, VehicleInput } from "../src/index.ts";

function station(power: number[], price: number[]): StationProfile {
  return { slotCount: power.length, powerLimitPerSlot: power, pricePerSlot: price };
}

function vehicle(partial: Partial<VehicleInput> & { id: string }): VehicleInput {
  return {
    arrivalSlot: 0,
    departureSlot: 4,
    demandEnergy: 10,
    maxPowerPerSlot: 10,
    ...partial,
  };
}

function assertPlanConsistent(plan: ChargePlan, vehicles: VehicleInput[], st: StationProfile): void {
  const usedPerSlot = new Array<number>(st.slotCount).fill(0);
  for (const v of vehicles) {
    const s = plan.schedules.find((x) => x.vehicleId === v.id);
    assert.ok(s, `缺少车辆 ${v.id} 的计划`);
    for (const a of s!.allocations) {
      assert.ok(a.slot >= v.arrivalSlot && a.slot < v.departureSlot, `${v.id} 在窗口外充电`);
      assert.ok(a.energy <= v.maxPowerPerSlot, `${v.id} 超过每格最大功率`);
      assert.ok(Number.isInteger(a.energy), `${v.id} 能量非整数`);
      usedPerSlot[a.slot] += a.energy;
    }
    const delivered = s!.allocations.reduce((sum, a) => sum + a.energy, 0);
    assert.equal(delivered, s!.deliveredEnergy);
    if (v.maxStarts !== undefined) {
      assert.ok(s!.sessions.length <= v.maxStarts, `${v.id} 启动次数超限`);
    }
    // 会话必须与分配一致且连续
    const sessionEnergy = s!.sessions.reduce((sum, x) => sum + x.energy, 0);
    assert.equal(sessionEnergy, delivered);
    for (const sess of s!.sessions) {
      for (let t = sess.startSlot; t < sess.endSlot; t++) {
        assert.ok(s!.allocations.some((a) => a.slot === t), `${v.id} 会话不连续`);
      }
    }
  }
  for (let t = 0; t < st.slotCount; t++) {
    assert.ok(usedPerSlot[t] <= st.powerLimitPerSlot[t], `时间格 ${t} 超出站点功率`);
  }
}

test("基本场景：满足需求且费用正确", () => {
  const st = station([10, 10, 10, 10], [5, 5, 5, 5]);
  const vs = [vehicle({ id: "A", demandEnergy: 20 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalDelivered, 20);
  assert.equal(plan.totalCost, 100);
  assert.equal(plan.windowDeficits.length, 0);
  assertPlanConsistent(plan, vs, st);
});

test("低价时段优先充电", () => {
  const st = station([10, 10, 10, 10], [9, 1, 1, 9]);
  const vs = [vehicle({ id: "A", demandEnergy: 20 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalCost, 20); // 全部落在电价 1 的两格
  const slots = plan.schedules[0].allocations.map((a) => a.slot);
  assert.deepEqual(slots, [1, 2]);
});

test("站点功率约束迫使多车分时共享", () => {
  const st = station([5, 5, 5, 5], [1, 1, 1, 1]);
  const vs = [
    vehicle({ id: "A", demandEnergy: 10 }),
    vehicle({ id: "B", demandEnergy: 10 }),
  ];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalDelivered, 20);
  assertPlanConsistent(plan, vs, st);
});

test("需求超过站点容量：无解并报告时间窗能量缺口", () => {
  const st = station([10, 10, 10, 10], [1, 1, 1, 1]);
  const vs = [vehicle({ id: "A", departureSlot: 2, demandEnergy: 100 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, false);
  assert.equal(plan.totalDelivered, 20);
  assert.deepEqual(plan.unmetDemand, [
    { vehicleId: "A", requested: 100, delivered: 20, missing: 80 },
  ]);
  const horn = plan.windowDeficits.find((d) => d.kind === "station-capacity");
  assert.ok(horn, "应报告站点容量缺口");
  assert.equal(horn!.startSlot, 0);
  assert.equal(horn!.endSlot, 2);
  assert.equal(horn!.requiredEnergy, 100);
  assert.equal(horn!.availableEnergy, 20);
  assert.equal(horn!.deficitEnergy, 80);
  const power = plan.windowDeficits.find((d) => d.kind === "vehicle-power");
  assert.ok(power, "应报告车辆功率缺口");
  assert.equal(power!.deficitEnergy, 80);
  assertPlanConsistent(plan, vs, st);
});

test("多车共享瓶颈区间：缺口按整个时间窗聚合", () => {
  // 三车窗口均为 [0,3)，总需求 30，站点容量 3*5=15，缺口 15
  const st = station([5, 5, 5, 0], [1, 1, 1, 1]);
  const vs = [
    vehicle({ id: "A", departureSlot: 3, demandEnergy: 10 }),
    vehicle({ id: "B", departureSlot: 3, demandEnergy: 10 }),
    vehicle({ id: "C", departureSlot: 3, demandEnergy: 10 }),
  ];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, false);
  const horn = plan.windowDeficits.find((d) => d.kind === "station-capacity");
  assert.ok(horn);
  assert.equal(horn!.startSlot, 0);
  assert.equal(horn!.endSlot, 3);
  assert.equal(horn!.deficitEnergy, 15);
  assert.equal(plan.unmetDemand.reduce((s, u) => s + u.missing, 0), 15);
  assertPlanConsistent(plan, vs, st);
});

test("启动次数限制：碎片会话被合并", () => {
  // 电价交错，最优松弛解会选 0/2/4 三个低价格（3 次启动）
  const st = station([10, 10, 10, 10, 10, 10], [1, 2, 1, 2, 1, 2]);
  const vs = [vehicle({ id: "A", departureSlot: 6, demandEnergy: 30, maxStarts: 1 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.schedules[0].sessions.length, 1);
  // 最优单会话为连续 3 格，费用 (1+2+1)*10 = 40
  assert.equal(plan.totalCost, 40);
  assertPlanConsistent(plan, vs, st);
});

test("启动次数限制：允许两次启动时保留低价格", () => {
  const st = station([10, 10, 10, 10, 10, 10], [1, 2, 1, 2, 1, 2]);
  const vs = [vehicle({ id: "A", departureSlot: 6, demandEnergy: 20, maxStarts: 2 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalCost, 20); // 两个最低价格即可
  assert.ok(plan.schedules[0].sessions.length <= 2);
  assertPlanConsistent(plan, vs, st);
});

test("启动次数限制下无解：报告 start-limit 缺口", () => {
  // 站点仅在 0/2/4 格有功率，单会话最多覆盖 1 格 = 10 能量，需求 30
  const st = station([10, 0, 10, 0, 10], [1, 1, 1, 1, 1]);
  const vs = [vehicle({ id: "A", departureSlot: 5, demandEnergy: 30, maxStarts: 1 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, false);
  const deficit = plan.windowDeficits.find((d) => d.kind === "start-limit");
  assert.ok(deficit, "应报告启动限制缺口");
  assert.equal(deficit!.deficitEnergy, 20);
  assert.equal(plan.unmetDemand[0].missing, 20);
  assertPlanConsistent(plan, vs, st);
});

test("到离站窗口约束：窗口外不充电", () => {
  const st = station([10, 10, 10, 10, 10, 10], [1, 1, 1, 1, 1, 1]);
  const vs = [vehicle({ id: "A", arrivalSlot: 2, departureSlot: 4, demandEnergy: 15 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  for (const a of plan.schedules[0].allocations) {
    assert.ok(a.slot >= 2 && a.slot < 4);
  }
  assertPlanConsistent(plan, vs, st);
});

test("零需求车辆不产生充电", () => {
  const st = station([10, 10], [1, 1]);
  const vs = [vehicle({ id: "A", departureSlot: 2, demandEnergy: 0 })];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalCost, 0);
  assert.equal(plan.schedules[0].allocations.length, 0);
});

test("错峰电价下多车按需求分配低价容量", () => {
  // 低价格容量 10，两车各需 10：一车拿低价，一车被迫用高价
  const st = station([10, 10], [1, 5]);
  const vs = [
    vehicle({ id: "A", departureSlot: 2, demandEnergy: 10 }),
    vehicle({ id: "B", departureSlot: 2, demandEnergy: 10 }),
  ];
  const plan = scheduleCharging(vs, st);
  assert.equal(plan.feasible, true);
  assert.equal(plan.totalCost, 10 * 1 + 10 * 5);
  assertPlanConsistent(plan, vs, st);
});

test("非法输入抛出异常", () => {
  const st = station([10, 10], [1, 1]);
  assert.throws(() => scheduleCharging([vehicle({ id: "A", demandEnergy: 1.5 })], st));
  assert.throws(() => scheduleCharging([vehicle({ id: "A", departureSlot: 5 })], st));
  assert.throws(() => scheduleCharging([vehicle({ id: "A", maxStarts: 0 })], st));
  assert.throws(() =>
    scheduleCharging([vehicle({ id: "A", departureSlot: 2 })], {
      slotCount: 2,
      powerLimitPerSlot: [10],
      pricePerSlot: [1, 1],
    }),
  );
});

test("较大规模随机算例的自洽性", () => {
  const T = 96;
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  const power = Array.from({ length: T }, () => 50 + Math.floor(rand() * 50));
  const price = Array.from({ length: T }, (_, t) => (t % 24 < 8 ? 1 : 3));
  const st = station(power, price);
  const vs: VehicleInput[] = [];
  for (let i = 0; i < 40; i++) {
    const arrival = Math.floor(rand() * (T - 8));
    vs.push({
      id: `V${i}`,
      arrivalSlot: arrival,
      departureSlot: arrival + 4 + Math.floor(rand() * 4),
      demandEnergy: Math.floor(rand() * 30),
      maxPowerPerSlot: 5 + Math.floor(rand() * 10),
      maxStarts: rand() < 0.5 ? 1 + Math.floor(rand() * 3) : undefined,
    });
  }
  const plan = scheduleCharging(vs, st);
  assertPlanConsistent(plan, vs, st);
  if (!plan.feasible) {
    assert.ok(plan.windowDeficits.length > 0, "无解时应给出缺口诊断");
  }
});
