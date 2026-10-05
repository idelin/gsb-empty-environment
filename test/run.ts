import { readFileSync } from "node:fs";
import { simulate } from "../src/simulate.ts";
import { solve } from "../src/solver.ts";
import type { Move, Scenario } from "../src/types.ts";

let passed = 0;
let failed = 0;

function check(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`  ok - ${name}`);
  } catch (e) {
    failed++;
    console.error(`  FAIL - ${name}: ${(e as Error).message}`);
  }
}

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(msg);
}

function assertThrows(re: RegExp, fn: () => void, msg: string): void {
  try {
    fn();
  } catch (e) {
    if (re.test((e as Error).message)) return;
    throw new Error(`${msg}（抛出了其他错误: ${(e as Error).message}）`);
  }
  throw new Error(`${msg}（未抛出异常）`);
}

const base: Scenario = {
  stations: [{ name: "S1", capacity: 10, initial: 8, target: 5 }],
  vehicle: { capacity: 4 },
  depot: { supply: 0, canUnload: true },
  costs: { perKm: 1, shortagePerBike: 10 },
  distanceMatrix: [
    [0, 2],
    [2, 0],
  ],
};

console.log("运行测试...");

check("手算最优：盈余站取 3 辆回仓，成本 4", () => {
  // 超量罚金 10/辆：不动车罚 30，取走 3 辆回仓里程 4km 成本 4
  const sc: Scenario = { ...base, costs: { perKm: 1, shortagePerBike: 10, overagePerBike: 10 } };
  const plan = solve(sc, { transferMode: "exact" });
  assert(plan.shortageBikes === 0, `缺车应为 0，实际 ${plan.shortageBikes}`);
  assert(Math.abs(plan.totalCost - 4) < 1e-9, `总成本应为 4，实际 ${plan.totalCost}`);
});

check("仓库补货：从仓库装 3 辆送缺车站，成本 4", () => {
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 0, target: 3 }],
    depot: { supply: 5 },
  };
  const plan = solve(sc, { transferMode: "exact" });
  assert(plan.shortageBikes === 0, `缺车应为 0，实际 ${plan.shortageBikes}`);
  assert(Math.abs(plan.totalCost - 4) < 1e-9, `总成本应为 4，实际 ${plan.totalCost}`);
});

check("全部达标时不动车，成本 0", () => {
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 5, target: 5 }],
  };
  const plan = solve(sc);
  assert(plan.moves.length === 0, `应为空方案，实际 ${plan.moves.length} 步`);
  assert(plan.totalCost === 0, `总成本应为 0，实际 ${plan.totalCost}`);
});

check("缺车补不齐时选择罚金与里程的平衡", () => {
  // 缺 3 辆，仓库无货可取，什么都做不了：罚金 30
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 0, target: 3 }],
    depot: { supply: 0 },
  };
  const plan = solve(sc, { transferMode: "exact" });
  assert(plan.shortageBikes === 3, `缺车应为 3，实际 ${plan.shortageBikes}`);
  assert(Math.abs(plan.totalCost - 30) < 1e-9, `总成本应为 30，实际 ${plan.totalCost}`);
});

check("禁止负库存：不能提前取走尚未送达的车", () => {
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 0, target: 0 }],
  };
  const bad: Move[] = [{ from: 0, to: 1, transfer: -1, distance: 2 }];
  assertThrows(/负库存/, () => simulate(sc, bad), "取走不存在的车应报错");
});

check("禁止超载", () => {
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 8, target: 0 }],
    depot: { supply: 0 },
  };
  const bad: Move[] = [{ from: 0, to: 1, transfer: -5, distance: 2 }];
  assertThrows(/载重上限/, () => simulate(sc, bad), "取 5 辆超过载重 4 应报错");
});

check("禁止站点超容量", () => {
  const sc: Scenario = {
    ...base,
    stations: [{ name: "S1", capacity: 10, initial: 8, target: 0 }],
    vehicle: { capacity: 4, initialLoad: 4 },
  };
  const bad: Move[] = [{ from: 0, to: 1, transfer: 4, distance: 2 }];
  assertThrows(/超过容量/, () => simulate(sc, bad), "放下后超过站点容量应报错");
});

check("车辆必须回仓", () => {
  const bad: Move[] = [{ from: 0, to: 1, transfer: -1, distance: 2 }];
  assertThrows(/回到仓库/, () => simulate(base, bad), "未回仓应报错");
});

check("仓库备货有限时不能超取", () => {
  const sc: Scenario = { ...base, depot: { supply: 1 } };
  const bad: Move[] = [{ from: 0, to: 0, transfer: -2, distance: 0 }];
  assertThrows(/备货不足/, () => simulate(sc, bad), "仓库只有 1 辆却装 2 辆应报错");
});

const example: Scenario = JSON.parse(readFileSync("scenarios/example.json", "utf8"));
const subset = (k: number): Scenario => {
  const idx = [0, ...Array.from({ length: k }, (_, i) => i + 1)];
  return {
    ...example,
    stations: example.stations.slice(0, k),
    distanceMatrix: idx.map((i) => idx.map((j) => example.distanceMatrix[i][j])),
  };
};

check("exact 与 candidates 在 4 站场景结果一致", () => {
  const a = solve(subset(4), { transferMode: "exact" });
  const b = solve(subset(4), { transferMode: "candidates" });
  assert(
    Math.abs(a.totalCost - b.totalCost) < 1e-6,
    `exact=${a.totalCost} candidates=${b.totalCost}`,
  );
});

check("exact 与 candidates 在 5 站场景结果一致", () => {
  const a = solve(subset(5), { transferMode: "exact" });
  const b = solve(subset(5), { transferMode: "candidates" });
  assert(
    Math.abs(a.totalCost - b.totalCost) < 1e-6,
    `exact=${a.totalCost} candidates=${b.totalCost}`,
  );
});

check("6 站示例：方案合法、零缺车、成本口径一致", () => {
  const plan = solve(example);
  assert(plan.shortageBikes === 0, `缺车应为 0，实际 ${plan.shortageBikes}`);
  const again = simulate(example, plan.moves).plan;
  assert(
    Math.abs(again.totalCost - plan.totalCost) < 1e-9,
    `重放成本 ${again.totalCost} 与 ${plan.totalCost} 不一致`,
  );
  const last = plan.moves[plan.moves.length - 1];
  assert(last === undefined || last.to === 0, "最后一步必须回仓");
  plan.finalInventories.forEach((v, i) => {
    assert(v >= 0 && v <= example.stations[i].capacity, `站点 ${i} 期末库存越界`);
  });
});

check("8 站大场景（candidates）：方案合法、零缺车", () => {
  const large: Scenario = JSON.parse(readFileSync("scenarios/example-large.json", "utf8"));
  const plan = solve(large);
  assert(plan.shortageBikes === 0, `缺车应为 0，实际 ${plan.shortageBikes}`);
  simulate(large, plan.moves); // 不抛异常即合法
});

check("多次访问同站是允许的", () => {
  // 载重 2，需要送 4 辆到 S1：必须跑两趟
  const sc: Scenario = {
    stations: [{ name: "S1", capacity: 10, initial: 0, target: 4 }],
    vehicle: { capacity: 2 },
    depot: { supply: 4 },
    costs: { perKm: 1, shortagePerBike: 100 },
    distanceMatrix: [
      [0, 1],
      [1, 0],
    ],
  };
  const plan = solve(sc, { transferMode: "exact" });
  assert(plan.shortageBikes === 0, `缺车应为 0，实际 ${plan.shortageBikes}`);
  const visits = plan.moves.filter((m) => m.to === 1).length;
  assert(visits === 2, `应访问 S1 两次，实际 ${visits} 次`);
  assert(Math.abs(plan.totalCost - 4) < 1e-9, `总成本应为 4，实际 ${plan.totalCost}`);
});

console.log(`\n${passed} 通过, ${failed} 失败`);
if (failed > 0) process.exit(1);
