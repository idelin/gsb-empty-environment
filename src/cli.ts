/**
 * 命令行入口：
 *   node src/cli.ts <input.json>
 *   cat input.json | node src/cli.ts
 *
 * 输入 JSON 结构见 Problem（src/scheduler.ts）与 example.json。
 */
import { readFileSync } from "node:fs";
import { solve, type Problem, type Solution } from "./scheduler.ts";

const readInput = (): string => {
  const path = process.argv[2];
  if (path && path !== "-") return readFileSync(path, "utf8");
  return readFileSync(0, "utf8"); // stdin
};

const pad = (value: string | number, width: number): string =>
  String(value).padStart(width);

const render = (solution: Solution): string => {
  const lines: string[] = [];
  lines.push(
    `${pad("序", 3)} ${pad("任务", 8)} ${pad("换线", 6)} ${pad("等待", 6)} ` +
      `${pad("开工", 6)} ${pad("完工", 6)} ${pad("交期", 6)} ${pad("迟交", 6)} ${pad("罚金", 8)}`,
  );
  for (const seg of solution.segments) {
    lines.push(
      `${pad(seg.seq, 3)} ${pad(seg.jobId, 8)} ${pad(seg.setupTime, 6)} ` +
        `${pad(seg.waitTime, 6)} ${pad(seg.startTime, 6)} ${pad(seg.endTime, 6)} ` +
        `${pad(seg.dueDate, 6)} ${pad(seg.tardiness, 6)} ${pad(seg.penalty, 8)}`,
    );
  }
  lines.push(`总罚金: ${solution.totalPenalty}`);
  lines.push(`最终完工时刻: ${solution.completionTime}`);
  return lines.join("\n");
};

const problem = JSON.parse(readInput()) as Problem;
const solution = solve(problem);
console.log(render(solution));
