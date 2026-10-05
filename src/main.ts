import * as fs from "fs";
import * as path from "path";
import { Scenario, Solution, solve, verify } from "./scheduler";
import { sampleScenario } from "./sample";

function pad(value: number): string {
  return String(value).padStart(3, " ");
}

function printSolution(scenario: Scenario, solution: Solution): void {
  const thresholdOf = new Map(scenario.models.map((m) => [m.name, m.threshold]));

  console.log("=== 最优排程方案 ===");
  console.log(`接单 ${solution.accepted.length} 单, 拒单 ${solution.rejected.length} 单, 总拒单损失: ${solution.totalLoss}`);
  if (solution.rejected.length > 0) {
    console.log(
      "被拒订单: " + solution.rejected.map((o) => `${o.id}(损失${o.penalty})`).join(", ")
    );
  }

  console.log("\n--- 设备时间线 (租赁 + 维护) ---");
  for (const unit of solution.units) {
    const threshold = thresholdOf.get(unit.model);
    console.log(`${unit.unitId} [型号 ${unit.model}, 阈值 ${threshold}]:`);
    if (unit.events.length === 0) {
      console.log("  (空闲, 未排产)");
      continue;
    }
    let cum = 0;
    for (const e of unit.events) {
      if (e.type === "rental") {
        cum += e.end - e.start;
        console.log(`  [${pad(e.start)}, ${pad(e.end)}) 租赁 订单${e.orderId}  累计 ${cum}/${threshold}`);
      } else {
        cum = 0;
        console.log(`  [${pad(e.start)}, ${pad(e.end)}) 维护 @工位${e.stationId}  累计清零`);
      }
    }
  }

  console.log("\n--- 工位时间线 (维护) ---");
  for (let sid = 1; sid <= scenario.stationCount; sid++) {
    const rows: { start: number; end: number; unitId: string }[] = [];
    for (const unit of solution.units) {
      for (const e of unit.events) {
        if (e.type === "maintenance" && e.stationId === sid) {
          rows.push({ start: e.start, end: e.end, unitId: unit.unitId });
        }
      }
    }
    rows.sort((a, b) => a.start - b.start);
    console.log(`工位${sid}:`);
    if (rows.length === 0) {
      console.log("  (无维护任务)");
    }
    for (const r of rows) {
      console.log(`  [${pad(r.start)}, ${pad(r.end)}) 维护 ${r.unitId}`);
    }
  }
}

function main(): void {
  const arg = process.argv[2];
  let scenario: Scenario;
  if (arg) {
    const file = path.resolve(arg);
    scenario = JSON.parse(fs.readFileSync(file, "utf-8")) as Scenario;
    console.log(`已加载场景: ${file}`);
  } else {
    scenario = sampleScenario;
    console.log("未指定场景文件, 使用内置示例 (用法: node dist/src/main.js <scenario.json>)");
  }

  const startedAt = Date.now();
  const solution = solve(scenario);
  const elapsed = Date.now() - startedAt;

  printSolution(scenario, solution);

  const problems = verify(scenario, solution);
  if (problems.length > 0) {
    console.error("\n方案校验失败:");
    for (const p of problems) console.error("  - " + p);
    process.exitCode = 1;
  } else {
    console.log(`\n方案校验通过 (求解耗时 ${elapsed}ms)`);
  }
}

main();
