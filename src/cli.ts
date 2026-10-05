import { readFileSync } from "node:fs";
import { parseFormula } from "./formula.ts";
import type { Formula } from "./formula.ts";
import { checkIntersection, MAX_NODES } from "./check.ts";
import type { CheckResult } from "./check.ts";

type CheckKind = "rw" | "ww" | "both";

function usage(): never {
  console.error("用法: node src/cli.ts <config.json|-> [--check rw|ww|both]");
  console.error("  config.json 形如:");
  console.error('  { "nodes": 5,');
  console.error('    "read":  { "atLeast": 3, "group": [0,1,2,3,4] },');
  console.error('    "write": { "all": [ { "k": 2, "of": [0,1,2] }, { "any": [3,4] } ] } }');
  process.exit(2);
}

function main(): void {
  const args = process.argv.slice(2);
  let file: string | null = null;
  let check: CheckKind = "both";
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--check") {
      const v = args[++i];
      if (v !== "rw" && v !== "ww" && v !== "both") usage();
      check = v;
    } else if (args[i].startsWith("--")) {
      usage();
    } else if (file === null) {
      file = args[i];
    } else {
      usage();
    }
  }
  if (file === null) usage();

  let config: Record<string, unknown>;
  try {
    const text = file === "-" ? readFileSync(0, "utf8") : readFileSync(file, "utf8");
    config = JSON.parse(text) as Record<string, unknown>;
  } catch (e) {
    console.error(`读取配置失败: ${(e as Error).message}`);
    process.exit(2);
  }

  const nodes = config.nodes;
  if (typeof nodes !== "number" || !Number.isInteger(nodes) || nodes < 1 || nodes > MAX_NODES) {
    console.error(`nodes 必须是 [1, ${MAX_NODES}] 内的整数`);
    process.exit(2);
  }

  let read: Formula | null = null;
  let write: Formula | null = null;
  try {
    if ("read" in config) read = parseFormula(config.read, nodes, "$.read");
    if ("write" in config) write = parseFormula(config.write, nodes, "$.write");
  } catch (e) {
    console.error(`公式解析失败: ${(e as Error).message}`);
    process.exit(2);
  }

  const wantRw = check === "rw" || check === "both";
  const wantWw = check === "ww" || check === "both";
  if (wantRw && (read === null || write === null)) {
    console.error("rw 检查需要同时提供 read 与 write 公式");
    process.exit(2);
  }
  if (wantWw && write === null) {
    console.error("ww 检查需要提供 write 公式");
    process.exit(2);
  }

  const checks: Record<string, CheckResult> = {};
  if (wantRw && read !== null && write !== null) {
    checks.rw = checkIntersection(read, write, nodes);
  }
  if (wantWw && write !== null) {
    checks.ww = checkIntersection(write, write, nodes);
  }

  const out = { nodes, checks };
  console.log(JSON.stringify(out, null, 2));

  const allIntersect = Object.values(checks).every((c) => c.intersects);
  process.exit(allIntersect ? 0 : 1);
}

main();
