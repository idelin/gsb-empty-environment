#!/usr/bin/env node
/**
 * 用法: node src/cli.ts <input.json> [--no-files]
 *
 * 输入 JSON:
 * {
 *   "files": { "path/to/file": "内容..." },
 *   "maxSelected": 25,               // 可选，默认 25
 *   "suggestions": [ FixSuggestion... ]
 * }
 *
 * 输出 JSON: CombineResult（--no-files 时不输出应用后的文件内容）
 */
import { readFileSync } from "node:fs";
import { selectCompatibleFixes } from "./select.ts";
import type { CombineInput } from "./types.ts";

function main(argv: string[]): number {
  const args = argv.slice(2);
  const includeFiles = !args.includes("--no-files");
  const path = args.find((a) => !a.startsWith("--"));
  if (!path) {
    console.error("用法: node src/cli.ts <input.json> [--no-files]");
    return 2;
  }
  let input: CombineInput;
  try {
    input = JSON.parse(readFileSync(path, "utf8")) as CombineInput;
  } catch (err) {
    console.error(`无法读取输入文件: ${(err as Error).message}`);
    return 2;
  }
  const result = selectCompatibleFixes(input);
  const output = includeFiles ? result : { ...result, files: undefined };
  console.log(JSON.stringify(output, null, 2));
  return 0;
}

process.exit(main(process.argv));
