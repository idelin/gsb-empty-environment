/** CLI: node dist/main.js [input.json]  (缺省运行内置示例) */
import * as fs from "fs";
import { Problem, Solution, solve } from "./scheduler";

export function formatSolution(sol: Solution): string {
  const lines: string[] = [];
  const pad = (v: string | number, w: number): string => String(v).padStart(w);
  lines.push(
    `${pad("#", 3)} ${pad("任务", 6)} ${pad("等待", 6)} ${pad("换线", 6)} ${pad("加工", 6)} ` +
    `${pad("开始", 6)} ${pad("完工", 6)} ${pad("期限", 6)} ${pad("迟交", 6)} ${pad("罚金", 8)}`
  );
  sol.segments.forEach((seg, i) => {
    const wait = seg.waitBeforeSetup + seg.waitAfterSetup;
    lines.push(
      `${pad(i + 1, 3)} ${pad(seg.jobId, 6)} ${pad(wait, 6)} ${pad(seg.setupLen, 6)} ` +
      `${pad(seg.end - seg.start, 6)} ${pad(seg.start, 6)} ${pad(seg.end, 6)} ` +
      `${pad(seg.due, 6)} ${pad(seg.tardiness, 6)} ${pad(seg.penalty, 8)}`
    );
  });
  lines.push(`顺序: ${sol.segments.map((s) => s.jobId).join(" -> ")}`);
  lines.push(`总罚金: ${sol.totalPenalty}   最大完工: ${sol.makespan}   总空闲: ${sol.totalIdle}`);
  lines.push(`(DP 考察状态数: ${sol.statesExplored})`);
  return lines.join("\n");
}

export function loadProblem(path: string): Problem {
  const raw = JSON.parse(fs.readFileSync(path, "utf8")) as Problem;
  if (!Array.isArray(raw.jobs) || !Array.isArray(raw.setup)) {
    throw new Error("输入需包含 jobs 数组与 setup 矩阵");
  }
  return raw;
}

const demo: Problem = {
  anticipatorySetup: true,
  initialSetup: [2, 1, 3, 0, 2, 1],
  jobs: [
    { id: "A", release: 0,  proc: 4, due: 14, weight: 3 },
    { id: "B", release: 6,  proc: 3, due: 12, weight: 5 },
    { id: "C", release: 2,  proc: 5, due: 22, weight: 1 },
    { id: "D", release: 10, proc: 2, due: 16, weight: 4 },
    { id: "E", release: 0,  proc: 6, due: 30, weight: 2 },
    { id: "F", release: 4,  proc: 3, due: 18, weight: 6 },
  ],
  setup: [
    [0, 3, 1, 4, 2, 5],
    [2, 0, 3, 1, 4, 2],
    [4, 2, 0, 3, 1, 3],
    [1, 4, 2, 0, 3, 2],
    [3, 1, 4, 2, 0, 4],
    [2, 3, 1, 4, 2, 0],
  ],
};

if (require.main === module) {
  const arg = process.argv[2];
  const problem = arg ? loadProblem(arg) : demo;
  if (!arg) console.log("(未提供输入文件, 运行内置示例)\n");
  const started = Date.now();
  const sol = solve(problem);
  console.log(formatSolution(sol));
  console.log(`求解耗时: ${Date.now() - started} ms`);
}
