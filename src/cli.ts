#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { compileProblem } from "./parse.ts";
import { renderReport } from "./report.ts";
import { solveProblem } from "./solve.ts";

function printUsage(): void {
  console.error(
    [
      "用法: node src/cli.ts [--json] <输入文件.json>",
      "      node src/cli.ts [--json] -        从标准输入读取",
      "",
      "输入格式见 README.md；退出码：0 修复成功，1 无解，2 输入错误。",
    ].join("\n"),
  );
}

function main(): number {
  const args = process.argv.slice(2);
  let jsonMode = false;
  const paths: string[] = [];
  for (const arg of args) {
    if (arg === "--json") {
      jsonMode = true;
    } else if (arg === "-h" || arg === "--help") {
      printUsage();
      return 0;
    } else {
      paths.push(arg);
    }
  }
  if (paths.length !== 1) {
    printUsage();
    return 2;
  }

  let raw: string;
  try {
    raw = paths[0] === "-" ? readFileSync(0, "utf8") : readFileSync(paths[0], "utf8");
  } catch {
    console.error(`无法读取输入文件：${paths[0]}`);
    return 2;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    console.error(`JSON 解析失败：${(err as Error).message}`);
    return 2;
  }

  try {
    const problem = compileProblem(parsed);
    const result = solveProblem(problem);
    if (jsonMode) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      console.log(renderReport(problem, result));
    }
    return result.satisfiable ? 0 : 1;
  } catch (err) {
    console.error(`输入错误：${(err as Error).message}`);
    return 2;
  }
}

process.exit(main());
