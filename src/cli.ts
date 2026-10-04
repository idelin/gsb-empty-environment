#!/usr/bin/env bun
import { readFileSync } from "node:fs";
import { analyze } from "./analyzer";
import { AnalyzerInput } from "./types";

function readInput(argv: string[]): string {
  const file = argv.find((a) => !a.startsWith("-"));
  if (file) return readFileSync(file, "utf8");
  return readFileSync(0, "utf8"); // stdin
}

const raw = readInput(process.argv.slice(2));
const input = JSON.parse(raw) as AnalyzerInput;
const result = analyze(input);
console.log(JSON.stringify(result, null, 2));

// 供 CI 使用：存在冲突时以非零码退出。
process.exit(result.conflicts.length > 0 ? 1 : 0);
