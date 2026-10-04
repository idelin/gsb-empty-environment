import {
  ProblemInstance,
  ScheduleResult,
  solve,
  validateSchedule,
} from "./scheduler";

/** 示例实例：两种型号、三台设备、两个维护工位、12 张订单 */
const problem: ProblemInstance = {
  models: [
    { id: "挖掘机", maintenanceThreshold: 10, maintenanceDuration: 3, stationId: "工位A" },
    { id: "吊车", maintenanceThreshold: 8, maintenanceDuration: 2, stationId: "工位B" },
  ],
  units: [
    { id: "挖-1", modelId: "挖掘机" },
    { id: "挖-2", modelId: "挖掘机", initialUsedHours: 4 },
    { id: "吊-1", modelId: "吊车" },
  ],
  orders: [
    { id: "O01", modelId: "挖掘机", start: 0, end: 6, rejectPenalty: 30 },
    { id: "O02", modelId: "挖掘机", start: 2, end: 8, rejectPenalty: 25 },
    { id: "O03", modelId: "吊车", start: 1, end: 7, rejectPenalty: 40 },
    { id: "O04", modelId: "挖掘机", start: 8, end: 12, rejectPenalty: 20 },
    { id: "O05", modelId: "吊车", start: 8, end: 14, rejectPenalty: 35 },
    { id: "O06", modelId: "挖掘机", start: 12, end: 18, rejectPenalty: 50 },
    { id: "O07", modelId: "挖掘机", start: 13, end: 17, rejectPenalty: 15 },
    { id: "O08", modelId: "吊车", start: 15, end: 20, rejectPenalty: 25 },
    { id: "O09", modelId: "挖掘机", start: 19, end: 25, rejectPenalty: 45 },
    { id: "O10", modelId: "吊车", start: 21, end: 27, rejectPenalty: 30 },
    { id: "O11", modelId: "挖掘机", start: 26, end: 30, rejectPenalty: 20 },
    { id: "O12", modelId: "吊车", start: 28, end: 33, rejectPenalty: 35 },
  ],
};

function printResult(instance: ProblemInstance, result: ScheduleResult): void {
  const modelById = new Map(instance.models.map((m) => [m.id, m]));

  console.log("=== 租赁与维护时间线（按设备） ===");
  for (const u of instance.units) {
    const m = modelById.get(u.modelId)!;
    console.log(`\n设备 ${u.id}（${u.modelId}，阈值 ${m.maintenanceThreshold}h，指定工位 ${m.stationId}）`);
    const events = [
      ...result.accepted.filter((r) => r.unitId === u.id),
      ...result.maintenances.filter((mt) => mt.unitId === u.id),
    ].sort((a, b) => a.start - b.start || a.end - b.end);
    if (events.length === 0) {
      console.log("  （空闲，未接单）");
      continue;
    }
    let used = u.initialUsedHours ?? 0;
    if (used > 0) console.log(`  初始累计工时 ${used}h`);
    for (const ev of events) {
      if (ev.kind === "rental") {
        used += ev.end - ev.start;
        console.log(`  租赁  订单 ${ev.orderId}  [${ev.start}, ${ev.end})   累计 ${used}h`);
      } else {
        used = 0;
        console.log(`  维护  工位 ${ev.stationId}  [${ev.start}, ${ev.end})   累计清零`);
      }
    }
  }

  console.log("\n=== 维护时间线（按工位） ===");
  const stationIds = [...new Set(instance.models.map((m) => m.stationId))];
  for (const sid of stationIds) {
    const evs = result.maintenances
      .filter((mt) => mt.stationId === sid)
      .sort((a, b) => a.start - b.start);
    console.log(`\n${sid}:`);
    if (evs.length === 0) console.log("  （无维护）");
    for (const ev of evs) console.log(`  [${ev.start}, ${ev.end})  设备 ${ev.unitId}`);
  }

  console.log("\n=== 拒单 ===");
  if (result.rejectedOrderIds.length === 0) console.log("  （无拒单）");
  for (const id of result.rejectedOrderIds) {
    const o = instance.orders.find((x) => x.id === id)!;
    console.log(`  ${id}（${o.modelId} [${o.start}, ${o.end})，损失 ${o.rejectPenalty}）`);
  }
  console.log(`\n总拒单损失: ${result.totalRejectLoss}`);
}

const result = solve(problem);
printResult(problem, result);

const errors = validateSchedule(problem, result);
if (errors.length > 0) {
  console.error("\n校验失败:");
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}
console.log("\n校验通过：排程满足全部规则。");
