// 法定集合相交性检查工具 CLI。
// 用法: node src/cli.ts <input.json> [--json]
// 退出码: 0 = 所有检查成立；1 = 存在不相交见证（反例）；2 = 用法/输入错误。

import { readFileSync } from "node:fs";
import { compileSpec, specToString, type Spec, type SpecJson } from "./spec.ts";
import { checkIntersection, type CheckOutcome, type SearchStats } from "./solver.ts";

interface InputFile {
  nodes: number | string[];
  read?: SpecJson;
  write: SpecJson;
}

function parseNodes(raw: number | string[]): string[] {
  if (typeof raw === "number") {
    if (!Number.isInteger(raw) || raw < 1) throw new Error("nodes 数量必须是正整数");
    return Array.from({ length: raw }, (_, i) => `n${i + 1}`);
  }
  if (Array.isArray(raw) && raw.length > 0 && raw.every((s) => typeof s === "string")) {
    if (new Set(raw).size !== raw.length) throw new Error("节点名必须唯一");
    return raw;
  }
  throw new Error("nodes 必须是正整数或非空节点名数组");
}

function fmtSet(indices: number[], names: string[]): string {
  return `[${indices.map((i) => names[i]).join(", ")}]`;
}

function fmtStats(stats: SearchStats): string {
  return (
    `展开搜索节点 ${stats.nodesExpanded} 个，剪枝 ${stats.prunes} 次，` +
    `检查候选 ${stats.candidatesVisited} 个，最大深度 ${stats.maxDepth}，` +
    `耗时 ${stats.elapsedMs.toFixed(1)}ms`
  );
}

function printOutcome(title: string, outcome: CheckOutcome, names: string[], aLabel: string, bLabel: string): void {
  if (outcome.holds) {
    console.log(`[${title}] 成立 ✓`);
    console.log(`  证明方法: ${outcome.proof!.method}`);
    console.log(`  ${outcome.proof!.detail}`);
    console.log(`  证明统计: ${fmtStats(outcome.proof!.stats)}`);
  } else {
    console.log(`[${title}] 不成立 ✗ — 找到字典序最小不相交见证`);
    console.log(`  ${aLabel} = ${fmtSet(outcome.witness!.a, names)}`);
    console.log(`  ${bLabel} = ${fmtSet(outcome.witness!.b, names)}`);
  }
}

function outcomeToJson(outcome: CheckOutcome, names: string[], aKey: string, bKey: string) {
  if (outcome.holds) {
    return { holds: true, proof: outcome.proof };
  }
  return {
    holds: false,
    witness: {
      [aKey]: outcome.witness!.a.map((i) => names[i]),
      [bKey]: outcome.witness!.b.map((i) => names[i]),
    },
  };
}

function main(): number {
  const args = process.argv.slice(2);
  const jsonMode = args.includes("--json");
  const files = args.filter((a) => a !== "--json");
  if (files.length !== 1) {
    console.error("用法: node src/cli.ts <input.json> [--json]");
    return 2;
  }

  let input: InputFile;
  try {
    input = JSON.parse(readFileSync(files[0], "utf8")) as InputFile;
  } catch (err) {
    console.error(`无法读取输入文件: ${(err as Error).message}`);
    return 2;
  }

  try {
    const names = parseNodes(input.nodes);
    const n = names.length;
    if (n > 20) {
      console.error(`警告: 节点数 ${n} 超出设计目标（<= 20），搜索可能变慢`);
    }
    const indexOf = new Map(names.map((name, i) => [name, i]));
    if (!input.write) throw new Error("缺少 write 规格");
    const writeSpec: Spec = compileSpec(input.write, indexOf, n);
    const readSpec: Spec | null = input.read ? compileSpec(input.read, indexOf, n) : null;

    const rw = readSpec ? checkIntersection(readSpec, writeSpec, n, "读", "写") : null;
    const ww = checkIntersection(writeSpec, writeSpec, n, "写", "写");

    if (jsonMode) {
      const report: Record<string, unknown> = {
        nodes: names,
        read: readSpec ? specToString(readSpec, names) : null,
        write: specToString(writeSpec, names),
        writeWrite: outcomeToJson(ww, names, "write1", "write2"),
      };
      if (rw) report.readWrite = outcomeToJson(rw, names, "read", "write");
      console.log(JSON.stringify(report, null, 2));
    } else {
      console.log("法定集合相交性检查");
      console.log("==================");
      console.log(`节点 (${n}): ${names.join(", ")}`);
      if (readSpec) console.log(`读集合: ${specToString(readSpec, names)}`);
      console.log(`写集合: ${specToString(writeSpec, names)}`);
      console.log("");
      if (rw) {
        printOutcome("读/写相交性", rw, names, "R", "W");
        console.log("");
      }
      printOutcome("写/写相交性", ww, names, "W1", "W2");
    }

    const allHold = (rw === null || rw.holds) && ww.holds;
    return allHold ? 0 : 1;
  } catch (err) {
    console.error(`输入错误: ${(err as Error).message}`);
    return 2;
  }
}

process.exit(main());
