import { strict as assert } from "assert";
import {
  MAX_ORDERS,
  OrderSpec,
  ProblemInstance,
  solve,
  validateSchedule,
} from "./scheduler";

function solved(problem: ProblemInstance) {
  const result = solve(problem);
  const errors = validateSchedule(problem, result);
  assert.deepEqual(errors, [], `排程校验失败: ${errors.join("; ")}`);
  return result;
}

// 1. 累计达阈值后必须显式维护；空闲不算维护完成
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 10, maintenanceDuration: 2, stationId: "S1" }],
    units: [{ id: "U1", modelId: "A" }],
    orders: [
      { id: "O1", modelId: "A", start: 0, end: 6, rejectPenalty: 5 },
      // O2 与 O1 之间有大段空闲，但累计 6+6=12 > 10，仍必须先维护
      { id: "O2", modelId: "A", start: 20, end: 26, rejectPenalty: 5 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 0);
  assert.equal(r.maintenances.length, 1, "空闲不能算作维护，必须有一次显式维护");
  const mt = r.maintenances[0];
  assert.equal(mt.stationId, "S1");
  assert.equal(mt.end - mt.start, 2);
  assert.ok(mt.start >= 6 && mt.end <= 20, "维护必须发生在两单之间");
  assert.equal(r.finalUsedHours["U1"], 6, "维护清零后只累计 O2 的 6 小时");
}

// 2. 同型号设备可互换：时间重叠的两单分给两台设备
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 10, maintenanceDuration: 2, stationId: "S1" }],
    units: [
      { id: "U1", modelId: "A" },
      { id: "U2", modelId: "A" },
    ],
    orders: [
      { id: "O1", modelId: "A", start: 0, end: 8, rejectPenalty: 9 },
      { id: "O2", modelId: "A", start: 2, end: 9, rejectPenalty: 9 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 0);
  assert.equal(r.accepted.length, 2);
  assert.notEqual(r.accepted[0].unitId, r.accepted[1].unitId);
}

// 3. 拒单损失最小：冲突订单中拒掉损失小的
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 100, maintenanceDuration: 2, stationId: "S1" }],
    units: [{ id: "U1", modelId: "A" }],
    orders: [
      { id: "cheap", modelId: "A", start: 0, end: 10, rejectPenalty: 3 },
      { id: "pricey", modelId: "A", start: 0, end: 10, rejectPenalty: 8 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 3);
  assert.deepEqual(r.rejectedOrderIds, ["cheap"]);
}

// 4. 累计工时跨订单保留
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 10, maintenanceDuration: 1, stationId: "S1" }],
    units: [{ id: "U1", modelId: "A" }],
    orders: [
      { id: "O1", modelId: "A", start: 0, end: 4, rejectPenalty: 5 },
      { id: "O2", modelId: "A", start: 5, end: 9, rejectPenalty: 5 },
      // 累计 4+4+4=12 > 10，O3 前必须维护
      { id: "O3", modelId: "A", start: 10, end: 14, rejectPenalty: 5 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 0);
  assert.equal(r.maintenances.length, 1);
  assert.deepEqual(
    r.maintenances.map((m) => [m.start, m.end]),
    [[9, 10]]
  );
  assert.equal(r.finalUsedHours["U1"], 4);
}

// 5. 同一指定工位同一时刻只能维护一台设备
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 4, maintenanceDuration: 3, stationId: "S1" }],
    units: [
      { id: "U1", modelId: "A" },
      { id: "U2", modelId: "A" },
    ],
    orders: [
      { id: "O1", modelId: "A", start: 0, end: 4, rejectPenalty: 9 },
      { id: "O2", modelId: "A", start: 0, end: 4, rejectPenalty: 9 },
      // 两台设备累计都到阈值，都要在 S1 串行维护（[4,7)、[7,10)）后才能接 O3/O4
      { id: "O3", modelId: "A", start: 10, end: 13, rejectPenalty: 9 },
      { id: "O4", modelId: "A", start: 10, end: 13, rejectPenalty: 9 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 0);
  assert.equal(r.maintenances.length, 2);
  const sorted = [...r.maintenances].sort((a, b) => a.start - b.start);
  assert.ok(sorted[1].start >= sorted[0].end, "同一工位的维护不能重叠");
}

// 6. 初始累计工时参与计算（跨订单保留的初始值）
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 10, maintenanceDuration: 2, stationId: "S1" }],
    units: [{ id: "U1", modelId: "A", initialUsedHours: 8 }],
    orders: [
      { id: "O1", modelId: "A", start: 3, end: 8, rejectPenalty: 7 },
      { id: "O2", modelId: "A", start: 10, end: 12, rejectPenalty: 7 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 0);
  assert.equal(r.maintenances.length, 1, "8+5>10，O1 前就需要先维护");
  assert.ok(r.maintenances[0].end <= 3, "维护必须在 O1 开始前完成");
}

// 7. 超过 15 单拒绝求解
{
  const orders: OrderSpec[] = [];
  for (let i = 0; i < MAX_ORDERS + 1; i++) {
    orders.push({ id: `O${i}`, modelId: "A", start: i * 10, end: i * 10 + 1, rejectPenalty: 1 });
  }
  assert.throws(
    () =>
      solve({
        models: [{ id: "A", maintenanceThreshold: 10, maintenanceDuration: 1, stationId: "S1" }],
        units: [{ id: "U1", modelId: "A" }],
        orders,
      }),
    /最多支持/
  );
}

// 8. 维护时间装不进间隙时只能拒单
{
  const problem: ProblemInstance = {
    models: [{ id: "A", maintenanceThreshold: 5, maintenanceDuration: 4, stationId: "S1" }],
    units: [{ id: "U1", modelId: "A" }],
    orders: [
      { id: "O1", modelId: "A", start: 0, end: 5, rejectPenalty: 10 },
      // 维护需要 4h，[5, 8) 之间只有 3h，装不下，只能拒单
      { id: "O2", modelId: "A", start: 8, end: 9, rejectPenalty: 6 },
    ],
  };
  const r = solved(problem);
  assert.equal(r.totalRejectLoss, 6);
  assert.deepEqual(r.rejectedOrderIds, ["O2"]);
}

console.log("全部测试通过。");
