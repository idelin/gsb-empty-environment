/**
 * 用法:
 *   node src/cli.ts input.json     从文件读取问题描述
 *   node src/cli.ts < input.json   从标准输入读取
 *
 * 退出码: 0 = 有解, 1 = 无解, 2 = 输入非法
 */
import { readFileSync } from "node:fs";
import { solve, validate, type Problem } from "./reorderer.ts";

function main(): void {
  const arg = process.argv[2];
  let raw: string;
  try {
    raw = arg ? readFileSync(arg, "utf8") : readFileSync(0, "utf8");
  } catch {
    console.error(`cannot read input ${arg ?? "<stdin>"}`);
    process.exit(2);
  }
  let problem: Problem;
  try {
    problem = JSON.parse(raw) as Problem;
  } catch (err) {
    console.error(`invalid JSON: ${(err as Error).message}`);
    process.exit(2);
  }
  const errors = validate(problem);
  if (errors.length > 0) {
    console.log(JSON.stringify({ status: "invalid", errors }, null, 2));
    process.exit(2);
  }
  const result = solve(problem);
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.status === "ok" ? 0 : 1);
}

main();
