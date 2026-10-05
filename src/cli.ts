#!/usr/bin/env bun
/**
 * CLI：从 JSON 文件（或 stdin）读取 { edges, query, directed? }，输出 JSON 结果。
 *
 * 用法：
 *   bun run src/cli.ts input.json
 *   cat input.json | bun run src/cli.ts
 *
 * 输入示例：
 * {
 *   "edges": [{ "from": "a", "to": "b", "start": 0, "end": 10 }],
 *   "query": { "source": "a", "target": "b", "start": 0, "end": 10 }
 * }
 */
import { readFileSync } from "node:fs";
import { checkConnectivity } from "./temporalConnectivity";
import type { CheckOptions, ConnectivityQuery, TemporalEdge } from "./temporalConnectivity";

interface CliInput {
  edges: TemporalEdge[];
  query: ConnectivityQuery;
  directed?: boolean;
}

function fail(message: string): never {
  console.error(`error: ${message}`);
  process.exit(2);
}

const arg = process.argv[2];
let raw: string;
try {
  raw = arg ? readFileSync(arg, "utf8") : readFileSync(0, "utf8");
} catch (err) {
  fail(`cannot read input: ${(err as Error).message}`);
}

let input: CliInput;
try {
  input = JSON.parse(raw!) as CliInput;
} catch {
  fail("input is not valid JSON");
}

if (!Array.isArray(input.edges) || !input.query) {
  fail('input must be { "edges": [...], "query": {...} }');
}

const options: CheckOptions = input.directed ? { directed: true } : {};
const result = checkConnectivity(input.edges, input.query, options);
console.log(JSON.stringify(result, null, 2));
process.exit(result.connected ? 0 : 1);
