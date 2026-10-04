import { readFileSync } from "node:fs";
import type { Problem } from "./types.ts";
import { solve } from "./solver.ts";

function main(): void {
  const args = process.argv.slice(2);
  const asJson = args.includes("--json");
  const file = args.find((a) => !a.startsWith("--"));
  if (!file) {
    console.error("用法: node src/cli.ts <问题.json> [--json]");
    process.exit(1);
  }
  const problem = JSON.parse(readFileSync(file, "utf8")) as Problem;
  const t0 = performance.now();
  const sol = solve(problem);
  const ms = (performance.now() - t0).toFixed(1);

  if (asJson) {
    console.log(JSON.stringify(sol, null, 2));
    return;
  }

  const byDecision = (want: string) =>
    problem.devices.filter((d) => sol.decisions[d.id] === want);

  console.log("=== 最优维修方案 ===");
  console.log(
    `预算: ${problem.budget}  已用: ${sol.budgetUsed}  剩余: ${problem.budget - sol.budgetUsed}`,
  );
  console.log(`恢复总价值: ${sol.totalValue}`);
  console.log(
    `修复 ${byDecision("repair").length} 台 / 拆解 ${byDecision("dismantle").length} 台 / 闲置 ${byDecision("idle").length} 台`,
  );

  console.log("\n-- 修复 --");
  for (const d of byDecision("repair")) {
    console.log(`  ${d.id}: 价值 ${d.repairedValue}，修复费 ${d.repairCost}`);
  }
  console.log("-- 拆解（损失残值，回收备件） --");
  for (const d of byDecision("dismantle")) {
    const got = Object.entries(d.contains)
      .map(([p, q]) => `${p}×${Math.floor(q * (d.recoveryRate ?? problem.recoveryRate ?? 1))}`)
      .filter((s) => !s.endsWith("×0"))
      .join(", ");
    console.log(`  ${d.id}: 损失残值 ${d.residualValue}，回收 ${got || "无"}`);
  }
  console.log("-- 闲置 --");
  console.log(`  ${byDecision("idle").map((d) => d.id).join(", ") || "无"}`);

  if (Object.keys(sol.purchases).length > 0) {
    console.log("-- 外购备件 --");
    for (const [part, qty] of Object.entries(sol.purchases)) {
      console.log(`  ${part} × ${qty}`);
    }
  }

  console.log("\n-- 备件流水（来源 -> 用途） --");
  for (const f of sol.flows) {
    console.log(`  ${f.from} --${f.part}×${f.qty}--> ${f.to}`);
  }
  if (sol.leftoverRecovered.length > 0) {
    console.log("-- 回收剩余（报废） --");
    for (const f of sol.leftoverRecovered) {
      console.log(`  ${f.from} --${f.part}×${f.qty}--> ${f.to}`);
    }
  }
  console.log(`\n搜索节点: ${sol.stats.nodes}，耗时 ${ms} ms`);
}

main();
