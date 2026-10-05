import * as fs from "fs";
import { solve } from "./solver";
import { Problem, Solution } from "./types";

function formatSolution(problem: Problem, sol: Solution): string {
  const lines: string[] = [];
  lines.push("========== 门店补货规划结果 ==========");
  lines.push("");
  lines.push("【箱型采购总量】");
  if (sol.totalBoxes.length === 0) {
    lines.push("  （不采购任何箱）");
  }
  for (const b of sol.totalBoxes) {
    lines.push(`  箱型 ${b.boxTypeId}: ${b.count} 箱（每箱 ${b.capacity} 件）`);
  }
  lines.push("");
  lines.push("【门店分配】（整箱归属单一门店，禁止拆箱跨店流转）");
  for (const p of sol.storePlans) {
    const store = problem.stores.find((s) => s.id === p.storeId)!;
    lines.push(`  门店 ${p.storeId}（需求 ${store.demand} 件）:`);
    if (p.boxes.length === 0) {
      lines.push("    配送: 无");
    } else {
      const desc = p.boxes.map((b) => `${b.boxTypeId}×${b.count}`).join(" + ");
      lines.push(`    配送: ${desc} = ${p.unitsSupplied} 件`);
    }
    lines.push(`    欠供: ${p.shortage} 件（罚金 ${p.shortageCost}），剩余库存: ${p.leftover} 件`);
    lines.push(
      `    成本: 采购 ${p.procurementCost} + 运输 ${p.transportCost} + 欠供罚金 ${p.shortageCost} = ${p.totalCost}`
    );
  }
  lines.push("");
  lines.push("【成本汇总】");
  lines.push(`  采购成本:   ${sol.totalProcurementCost}`);
  lines.push(`  运输成本:   ${sol.totalTransportCost}`);
  lines.push(`  欠供罚金:   ${sol.totalShortageCost}`);
  lines.push(`  总成本:     ${sol.totalCost}`);
  lines.push("");
  lines.push("【库存】");
  lines.push(`  剩余库存合计: ${sol.totalLeftover} 件；欠供合计: ${sol.totalShortage} 件`);
  return lines.join("\n");
}

function main(): void {
  const args = process.argv.slice(2);
  if (args.length !== 1) {
    console.error("用法: node dist/src/index.js <input.json>");
    process.exit(1);
  }
  const problem = JSON.parse(fs.readFileSync(args[0], "utf-8")) as Problem;
  const sol = solve(problem);
  console.log(formatSolution(problem, sol));
}

main();
