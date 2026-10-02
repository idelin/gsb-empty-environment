/** 运行样例:node src/demo.ts */

import { bruteForce, solve } from "./solver.ts";
import { buildSchedulingProblem, SLOT_NAMES, TASK_NAMES } from "./scheduling.ts";

const problem = buildSchedulingProblem();

console.log("=== 带权 Max-CSP 分支定界求解:任务排期 ===\n");
console.log("任务与可选时段: T1..T5 ∈ {1=周一上午, 2=周一下午, 3=周二上午}");
console.log("硬约束: H1(T1≠T2 设备A)  H2(T3≠T4 设备B)  H3(T5≠T2 人员)");
console.log("软约束: S1(w=10,T1→1) S2(w=8,T3→1) S3(w=3,T5→1) S4(w=2,T2→1) S5(w=1,T4→1)\n");

const result = solve(problem);

if (!result.assignment) {
  console.log("硬约束不可满足,无解。");
  process.exit(1);
}

console.log("--- 最优排期表(分支定界) ---");
for (const task of Object.keys(TASK_NAMES)) {
  const slot = result.assignment[task];
  console.log(`  ${task} ${TASK_NAMES[task]}  ->  ${SLOT_NAMES[slot]}`);
}

console.log(`\n违反的软约束: ${result.violated.join(", ")} (共 ${result.violated.length} 条)`);
for (const name of result.violated) {
  const sc = problem.soft.find((s) => s.name === name)!;
  console.log(`  ${name}: 惩罚权重 ${sc.weight}`);
}
console.log(`总惩罚代价: ${result.cost}`);
console.log(
  `搜索统计: 展开节点 ${result.stats.nodes} 个, 剪枝 ${result.stats.pruned} 次` +
    ` (全空间叶节点 ${3 ** 5} 个)`,
);

const bf = bruteForce(problem);
console.log(`\n暴力枚举交叉验证: 最优代价 ${bf.cost} (叶节点 ${bf.stats.nodes} 个)`);
console.log(
  bf.cost === result.cost
    ? "✔ 分支定界结果与全局枚举一致,确为全局最优。"
    : "✘ 结果不一致!",
);
