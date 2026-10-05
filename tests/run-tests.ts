import { Scenario, Solution, solve, verify } from "../src/scheduler";

let failures = 0;

function check(name: string, condition: boolean, detail?: string): void {
  if (condition) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.error(`FAIL ${name}${detail ? " -- " + detail : ""}`);
  }
}

function solveAndVerify(scenario: Scenario): Solution {
  const solution = solve(scenario);
  const problems = verify(scenario, solution);
  check("方案通过规则校验", problems.length === 0, problems.join("; "));
  return solution;
}

function maintenanceCount(solution: Solution): number {
  let n = 0;
  for (const u of solution.units) for (const e of u.events) if (e.type === "maintenance") n++;
  return n;
}

// 1) 累计跨订单保留, 达到阈值必须维护后才能继续接单
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 1, threshold: 10, maintenanceDuration: 4 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 6, penalty: 10 },
      { id: "B", model: "M", start: 7, end: 13, penalty: 10 },
      { id: "C", model: "M", start: 14, end: 20, penalty: 10 },
    ],
  };
  const sol = solveAndVerify(scenario);
  // 最优: 接 A、C, 在 [6,10) 维护, 拒 B, 损失 10
  check("触发维护后损失最小", sol.totalLoss === 10, `got ${sol.totalLoss}`);
  check("维护事件存在", maintenanceCount(sol) === 1);
  check("被拒的是 B", sol.rejected.length === 1 && sol.rejected[0].id === "B");
}

// 2) 同型号设备可互换: 同一时间窗的两单由两台设备分别承担
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 2, threshold: 100, maintenanceDuration: 1 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 5, penalty: 1 },
      { id: "B", model: "M", start: 0, end: 5, penalty: 1 },
    ],
  };
  const sol = solveAndVerify(scenario);
  check("同型号互换两单全接", sol.totalLoss === 0 && sol.accepted.length === 2);
}

// 3) 工位竞争: 两台设备都需要维护, 只有一个工位, 只能保一单, 拒损失小的
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 2, threshold: 4, maintenanceDuration: 5 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 4, penalty: 100 },
      { id: "B", model: "M", start: 0, end: 4, penalty: 100 },
      { id: "C", model: "M", start: 10, end: 14, penalty: 1 },
      { id: "D", model: "M", start: 10, end: 14, penalty: 90 },
    ],
  };
  const sol = solveAndVerify(scenario);
  check("工位竞争下拒最便宜的", sol.totalLoss === 1, `got ${sol.totalLoss}`);
  check("被拒的是 C", sol.rejected.length === 1 && sol.rejected[0].id === "C");
}

// 4) 空闲不等于维护: 长时间空闲后累计时长仍然保留, 必须显式维护
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 1, threshold: 6, maintenanceDuration: 2 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 6, penalty: 10 },
      { id: "B", model: "M", start: 20, end: 26, penalty: 10 },
    ],
  };
  const sol = solveAndVerify(scenario);
  check("长空闲后仍需显式维护", sol.totalLoss === 0 && maintenanceCount(sol) === 1);

  const tight: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 1, threshold: 6, maintenanceDuration: 2 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 6, penalty: 10 },
      { id: "B", model: "M", start: 7, end: 13, penalty: 5 },
    ],
  };
  const sol2 = solveAndVerify(tight);
  // 空隙只有 1 小时, 排不进 2 小时维护, 只能拒 B
  check("空隙排不进维护则拒单", sol2.totalLoss === 5, `got ${sol2.totalLoss}`);
}

// 5) 预防性维护: 工位稍后被占用时, 提前维护能救回高价值订单
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [
      { name: "M", units: 1, threshold: 10, maintenanceDuration: 4 },
      { name: "N", units: 1, threshold: 4, maintenanceDuration: 4 },
    ],
    orders: [
      { id: "A", model: "M", start: 0, end: 6, penalty: 10 },
      { id: "B", model: "M", start: 10, end: 13, penalty: 10 },
      { id: "C", model: "M", start: 20, end: 23, penalty: 100 },
      { id: "D", model: "N", start: 9, end: 13, penalty: 10 },
      { id: "E", model: "N", start: 20, end: 24, penalty: 90 },
    ],
  };
  const sol = solveAndVerify(scenario);
  // M 必须在 [6,10) 预防性维护(此时 cum=6 未超阈值), 否则 [13,17) 被 N 占用后 C 无法维护
  check("预防性维护实现零拒单", sol.totalLoss === 0, `got ${sol.totalLoss}`);
}

// 6) 订单时长超过阈值则永远无法服务
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 1, threshold: 5, maintenanceDuration: 1 }],
    orders: [{ id: "A", model: "M", start: 0, end: 6, penalty: 7 }],
  };
  const sol = solveAndVerify(scenario);
  check("超阈值订单必拒", sol.totalLoss === 7 && sol.rejected.length === 1);
}

// 7) 超过 15 单直接报错
{
  const orders = [];
  for (let i = 0; i < 16; i++) {
    orders.push({ id: "O" + i, model: "M", start: i * 10, end: i * 10 + 1, penalty: 1 });
  }
  let threw = false;
  try {
    solve({ stationCount: 1, models: [{ name: "M", units: 1, threshold: 10, maintenanceDuration: 1 }], orders });
  } catch (e) {
    threw = true;
  }
  check("超过 15 单抛错", threw);
}

// 8) 校验器能识别"把空闲当作维护"的非法方案
{
  const scenario: Scenario = {
    stationCount: 1,
    models: [{ name: "M", units: 1, threshold: 6, maintenanceDuration: 2 }],
    orders: [
      { id: "A", model: "M", start: 0, end: 6, penalty: 10 },
      { id: "B", model: "M", start: 20, end: 26, penalty: 10 },
    ],
  };
  const illegal: Solution = {
    totalLoss: 0,
    accepted: scenario.orders.slice(),
    rejected: [],
    units: [
      {
        unitId: "M#1",
        model: "M",
        events: [
          { type: "rental", orderId: "A", start: 0, end: 6 },
          { type: "rental", orderId: "B", start: 20, end: 26 },
        ],
      },
    ],
  };
  const problems = verify(scenario, illegal);
  check("校验器抓出空闲冒充维护", problems.length > 0, problems.join("; "));
}

// 9) 随机小场景: 最优解必须合法且不差于"全拒"
{
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let t = 0; t < 30; t++) {
    const models = [
      { name: "M", units: 1 + Math.floor(rand() * 2), threshold: 4 + Math.floor(rand() * 8), maintenanceDuration: 1 + Math.floor(rand() * 4) },
      { name: "N", units: 1 + Math.floor(rand() * 2), threshold: 4 + Math.floor(rand() * 8), maintenanceDuration: 1 + Math.floor(rand() * 4) },
    ];
    const orders = [];
    const n = 5 + Math.floor(rand() * 6);
    for (let i = 0; i < n; i++) {
      const start = Math.floor(rand() * 30);
      orders.push({
        id: "O" + i,
        model: rand() < 0.5 ? "M" : "N",
        start,
        end: start + 1 + Math.floor(rand() * 5),
        penalty: 1 + Math.floor(rand() * 50),
      });
    }
    const scenario: Scenario = { stationCount: 1 + Math.floor(rand() * 2), models, orders };
    const sol = solve(scenario);
    const problems = verify(scenario, sol);
    const totalPenalty = orders.reduce((s, o) => s + o.penalty, 0);
    if (problems.length > 0 || sol.totalLoss > totalPenalty) {
      check(`随机场景 #${t}`, false, problems.join("; ") + ` loss=${sol.totalLoss}`);
    }
  }
  check("随机场景全部合法且不劣于全拒", true);
}

if (failures > 0) {
  console.error(`\n${failures} 个断言失败`);
  process.exit(1);
}
console.log("\n全部测试通过");
