/**
 * 命令行入口：
 *   node src/cli.ts <input.json> [--json] [--dump-dp]
 *   cat input.json | node src/cli.ts -
 *
 * 输入 JSON 格式见 README.md / examples/sample.json。
 */

import { readFileSync } from "node:fs";
import {
  optimize,
  type OptimizeInput,
  type PlanNode,
  type OptimizeResult,
} from "./optimizer.ts";
import { Rational } from "./rational.ts";

function fmt(r: Rational): string {
  const exact = r.toString();
  const dec = r.toDecimal(6);
  return exact === dec ? exact : `${exact} (≈${dec})`;
}

function renderPlan(
  node: PlanNode,
  prefix: string,
  isLast: boolean,
  isRoot: boolean,
  lines: string[],
): void {
  const connector = isRoot ? "" : isLast ? "└─ " : "├─ ";
  if (node.kind === "leaf") {
    lines.push(`${prefix}${connector}${node.table}  基数=${fmt(node.cardinality)}`);
    return;
  }
  const joinLabel = node.isCrossProduct ? "⋈(笛卡尔积)" : "⋈";
  const parts = [
    `${joinLabel} {${node.tables.join(",")}}`,
    `基数=${fmt(node.cardinality)}`,
    `选择率=${node.joinSelectivity.toString()}`,
    `本步成本=${fmt(node.stepCost)}`,
  ];
  if (!node.materializationCost.isZero()) {
    parts.push(`其中物化=${fmt(node.materializationCost)}`);
  }
  parts.push(`累计成本=${fmt(node.cumulativeCost)}`);
  lines.push(`${prefix}${connector}${parts.join("  ")}`);
  const childPrefix = isRoot ? "" : prefix + (isLast ? "   " : "│  ");
  renderPlan(node.left, childPrefix, false, false, lines);
  renderPlan(node.right, childPrefix, true, false, lines);
}

function planToJson(node: PlanNode): unknown {
  if (node.kind === "leaf") {
    return { type: "scan", table: node.table, cardinality: node.cardinality.toString() };
  }
  return {
    type: "join",
    tables: node.tables,
    cardinality: node.cardinality.toString(),
    joinSelectivity: node.joinSelectivity.toString(),
    crossProduct: node.isCrossProduct,
    stepCost: node.stepCost.toString(),
    materializationCost: node.materializationCost.toString(),
    cumulativeCost: node.cumulativeCost.toString(),
    children: [planToJson(node.left), planToJson(node.right)],
  };
}

function printText(result: OptimizeResult, dumpDp: boolean): void {
  const lines: string[] = [];
  renderPlan(result.plan, "", true, true, lines);
  console.log("=== 最优计划 ===");
  console.log(lines.join("\n"));
  console.log("");
  console.log(`最优总成本: ${fmt(result.optimalCost)}`);
  console.log(
    `统计: 表数=${result.stats.tableCount} 子集数=${result.stats.subsetsEvaluated} ` +
      `可达子集=${result.stats.reachableSubsets} 考察的二分=${result.stats.splitsConsidered}`,
  );
  if (dumpDp) {
    console.log("");
    console.log("=== 各子计划估算（DP 表） ===");
    for (const est of result.estimates) {
      console.log(
        `{${est.tables.join(",")}}  基数=${fmt(est.cardinality)}  最优累计成本=${fmt(est.bestCost)}`,
      );
    }
  }
}

function main(): void {
  const args = process.argv.slice(2);
  const jsonMode = args.includes("--json");
  const dumpDp = args.includes("--dump-dp");
  const pathArg = args.find((a) => !a.startsWith("--"));
  if (!pathArg) {
    console.error("用法: node src/cli.ts <input.json|-> [--json] [--dump-dp]");
    process.exit(2);
  }
  const raw = pathArg === "-" ? readFileSync(0, "utf8") : readFileSync(pathArg, "utf8");
  const input = JSON.parse(raw) as OptimizeInput;
  const result = optimize(input);
  if (jsonMode) {
    console.log(
      JSON.stringify(
        {
          optimalCost: result.optimalCost.toString(),
          plan: planToJson(result.plan),
          stats: result.stats,
          estimates: result.estimates.map((e) => ({
            tables: e.tables,
            cardinality: e.cardinality.toString(),
            bestCost: e.bestCost.toString(),
          })),
        },
        null,
        2,
      ),
    );
  } else {
    printText(result, dumpDp);
  }
}

try {
  main();
} catch (err) {
  console.error(`错误: ${(err as Error).message}`);
  process.exit(1);
}
