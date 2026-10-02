/**
 * 运行样例：任务排期 Max-CSP 分支定界求解。
 * 运行方式：npm run demo
 */

import { buildSchedulingSolver, formatSchedule } from "./scheduling.ts";

const solver = buildSchedulingSolver();

console.log("=== 带权 Max-CSP 分支定界求解器：任务排期场景 ===\n");
console.log("变量：T1 需求评审 / T2 原型设计 / T3 版本上线 / T4 宣传物料 / T5 备份演练");
console.log("值域：0=周一 1=周二 2=周三 3=周四 4=周五（每任务占 1 个时段）\n");

console.log("硬约束（必须满足）：");
console.log("  H1 资源独占：同一时段最多一个任务");
console.log("  H2 硬性截止：需求评审 ≤ 周三");
console.log("  H3 合规前置：宣传物料 早于 版本上线\n");

console.log("软约束（违反计入惩罚）：");
console.log("  S1 权重10 需求评审=周一");
console.log("  S2 权重 8 原型设计=周二");
console.log("  S3 权重 2 版本上线=周三   （次要）");
console.log("  S4 权重 4 宣传物料=周四");
console.log("  S5 权重 1 备份演练=上线后第2天（次要）\n");

const solution = solver.solve();

if (solution === null) {
  console.log("问题不可行：硬约束无法同时满足。");
  process.exit(1);
}

console.log("--- 搜索统计（分支定界） ---");
console.log(`理论叶子空间 : ${solution.stats.totalLeafSpace} 个完整排期`);
console.log(`展开节点     : ${solution.stats.nodesExplored}`);
console.log(`下界剪枝     : ${solution.stats.nodesPruned} 棵子树`);
console.log(`硬约束剪枝   : ${solution.stats.hardPruned} 个分支`);
console.log(`实际访问叶子 : ${solution.stats.leavesVisited}\n`);

console.log("--- 全局最优排期表 ---");
console.log(formatSchedule(solution));
console.log();

console.log(`总惩罚（被违反软约束权重之和）: ${solution.cost}`);
console.log(`违反软约束 ${solution.violations.length} 条：`);
for (const v of solution.violations) {
  console.log(`  ✗ ${v.name}（权重 ${v.weight}）${v.description ?? ""}`);
}
console.log("满足的软约束：S1、S2、S4（权重 10、8、4）");
console.log("硬约束 H1、H2、H3 全部成立。");
