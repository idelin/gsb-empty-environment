import { solve, type TraceEvent } from "./solver.ts";
import { buildBenchmarkProblem } from "./problem.ts";

function render(e: TraceEvent): string {
  switch (e.type) {
    case "assign":
      return `  [第${e.level}步] 赋值 ${e.variable} = ${e.value}`;
    case "reject":
      return `  [第${e.level}步] 拒绝 ${e.variable} = ${e.value}  (与第${e.blamedLevel}步 ${e.blamedVariable} 冲突: ${e.constraint})`;
    case "dead-end":
      return `  [第${e.level}步] 死胡同! ${e.variable} 值域耗尽, 冲突集 = {${e.conflictSet.map((l) => `第${l}步`).join(", ")}}`;
    case "backjump":
      return e.width > 1
        ? `  >>> 回跳: 第${e.from}步 ==> 第${e.to}步 (跳过 ${e.width - 1} 层无关变量)`
        : `  >>> 回退: 第${e.from}步 -> 第${e.to}步`;
    case "solution":
      return `  [完成] 找到解: [${e.assignment.join(", ")}]`;
    case "unsat":
      return `  [完成] 问题不可满足`;
  }
}

const problem = buildBenchmarkProblem();

console.log("=== 10 变量 CSP：冲突导向回跳 (CBJ) ===");
const cbj = solve(problem, "cbj");
for (const e of cbj.events) console.log(render(e));
console.log(
  `  统计: 节点=${cbj.stats.nodes}, 约束检查=${cbj.stats.constraintChecks}, 死胡同=${cbj.stats.deadEnds}`,
);

console.log("\n=== 同一问题：传统时序回溯 (对照) ===");
const bt = solve(problem, "chronological");
const head = bt.events.slice(0, 14);
for (const e of head) console.log(render(e));
console.log(`  ... (中间省略 ${bt.events.length - 14} 条在 X3..X7 子树中的无用搜索) ...`);
for (const e of bt.events.slice(-3)) console.log(render(e));
console.log(
  `  统计: 节点=${bt.stats.nodes}, 约束检查=${bt.stats.constraintChecks}, 死胡同=${bt.stats.deadEnds}`,
);

console.log("\n=== 对比 ===");
console.log(
  `  CBJ 用 ${cbj.stats.nodes} 个节点 / ${cbj.stats.constraintChecks} 次约束检查求解；`,
);
console.log(
  `  时序回溯用 ${bt.stats.nodes} 个节点 / ${bt.stats.constraintChecks} 次约束检查；`,
);
console.log(
  `  剪枝效果: 节点减少 ${((1 - cbj.stats.nodes / bt.stats.nodes) * 100).toFixed(1)}%，`,
  `约束检查减少 ${((1 - cbj.stats.constraintChecks / bt.stats.constraintChecks) * 100).toFixed(1)}%。`,
);
