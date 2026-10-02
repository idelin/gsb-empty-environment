import { solveCBJ, solveChronological, varName, type SolveResult } from "./csp.ts";
import { buildDemoProblem } from "./demo-problem.ts";

function report(title: string, result: SolveResult, problemVarNames: (i: number) => string): void {
  console.log(`\n=== ${title} ===`);
  console.log(`访问节点数: ${result.stats.nodesVisited}`);
  if (result.stats.backjumps.length > 0) {
    console.log("回跳记录:");
    for (const j of result.stats.backjumps) {
      const conf = j.conflictSet.map(problemVarNames).join(", ");
      console.log(
        `  第 ${j.fromLevel + 1} 步(${problemVarNames(j.fromLevel)}) ` +
          `-> 第 ${j.toLevel + 1} 步(${problemVarNames(j.toLevel)})，` +
          `跳过 ${j.fromLevel - j.toLevel - 1} 层；conflict set = {${conf}}`,
      );
    }
  }
  console.log(`解: ${result.assignment ? JSON.stringify(result.assignment) : "无解"}`);
}

const problem = buildDemoProblem();
const name = (i: number) => varName(problem, i);

console.log("问题: 10 变量 CSP，X8 域={1}，关键约束 X8 ≠ X2，X3..X7 无约束");
report("Conflict-directed Backjumping (CBJ)", solveCBJ(problem), name);
report("Chronological Backtracking (对照)", solveChronological(problem), name);

const cbj = solveCBJ(problem);
const chr = solveChronological(problem);
console.log(
  `\n剪枝效果: 时序回溯访问 ${chr.stats.nodesVisited} 个节点，` +
    `CBJ 仅访问 ${cbj.stats.nodesVisited} 个节点，` +
    `减少 ${(((chr.stats.nodesVisited - cbj.stats.nodesVisited) / chr.stats.nodesVisited) * 100).toFixed(1)}%`,
);
