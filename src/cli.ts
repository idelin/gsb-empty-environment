import { readFileSync } from "node:fs";
import {
  optimize,
  optimizeLeftDeep,
  formatPlan,
  formatNumber,
  QuerySpec,
} from "./optimizer.js";

function printResult(title: string, query: QuerySpec, leftDeep: boolean): void {
  const result = leftDeep ? optimizeLeftDeep(query) : optimize(query);
  console.log("=== " + title + " ===");
  if (result.plan === null) {
    console.log("无可行计划（连接图不连通且禁止笛卡尔积）");
  } else {
    console.log(formatPlan(result.plan));
    console.log("最优总成本: " + formatNumber(result.cost));
  }
  console.log(
    "枚举统计: 子集 " + result.subsetsEvaluated + " 个, 划分 " + result.partitionsEvaluated + " 次"
  );
  console.log("");
}

function runDemo(): void {
  const demo: QuerySpec = {
    relations: [
      { name: "A", cardinality: 1000 },
      { name: "B", cardinality: 1000 },
      { name: "C", cardinality: 1000 },
      { name: "D", cardinality: 1000 },
    ],
    predicates: [
      { left: "A", right: "B", selectivity: 0.001 },
      { left: "C", right: "D", selectivity: 0.001 },
      { left: "B", right: "C", selectivity: 0.01 },
    ],
    options: {
      allowCrossProducts: false,
      materialization: { perTuple: 0, flat: 0 },
    },
  };
  printResult("bushy 枚举（最优）", demo, false);
  printResult("仅左深树（对比）", demo, true);

  const disconnected: QuerySpec = {
    relations: [
      { name: "X", cardinality: 500 },
      { name: "Y", cardinality: 800 },
      { name: "Z", cardinality: 300 },
    ],
    predicates: [{ left: "X", right: "Y", selectivity: 0.05 }],
    options: { allowCrossProducts: false },
  };
  printResult("禁止笛卡尔积 + 图不连通", disconnected, false);
  const allowed: QuerySpec = {
    relations: disconnected.relations,
    predicates: disconnected.predicates,
    options: { allowCrossProducts: true },
  };
  printResult("允许笛卡尔积 + 图不连通", allowed, false);
}

function main(): void {
  const file = process.argv[2];
  if (!file) {
    runDemo();
    return;
  }
  const query = JSON.parse(readFileSync(file, "utf8")) as QuerySpec;
  printResult("bushy 枚举（最优）", query, false);
  printResult("仅左深树（对比）", query, true);
}

main();
