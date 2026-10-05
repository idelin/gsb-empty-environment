import { readFileSync } from "node:fs";
import process from "node:process";
import type { Spec } from "./types.ts";
import { QueryError } from "./evaluate.ts";
import { findDifference, type DifferentResult, type SearchResult } from "./search.ts";
import { formatTuple } from "./util.ts";

function usage(exitCode: number): never {
  const text = [
    "用法: node src/cli.ts [选项] <spec.json>   (省略文件路径时从标准输入读取)",
    "",
    "选项:",
    "  --json               以 JSON 输出结果",
    "  --max-rows N         覆盖 spec 中的 maxRows",
    "  --max-databases N    放宽搜索空间上限(默认 2000000)",
    "  -h, --help           显示帮助",
    "",
    "退出码: 0 = 界内等价, 1 = 发现差异, 2 = 输入/运行错误",
  ].join("\n");
  (exitCode === 0 ? console.log : console.error)(text);
  process.exit(exitCode);
}

function renderDifferent(r: DifferentResult): string {
  const lines: string[] = [];
  lines.push(`发现差异(最小反例: 总行数 ${r.totalRows},同规模下字典序最小)`);
  if (r.note) lines.push(`  备注: ${r.note}`);
  lines.push(`  已检查数据库数: ${r.databasesChecked}`);
  lines.push("");
  lines.push("数据库:");
  for (const rel of r.database) {
    lines.push(`  ${rel.rel}(${rel.attrs.join(", ")}):`);
    if (rel.rows.length === 0) {
      lines.push("    <空>");
    } else {
      for (const row of rel.rows) lines.push(`    ${formatTuple(row)}`);
    }
  }
  lines.push("");
  lines.push(`Q1 结果(${r.q1.attrs.join(", ")}):`);
  if (r.q1.rows.length === 0) lines.push("  <空>");
  for (const row of r.q1.rows) lines.push(`  ${formatTuple(row)}`);
  lines.push(`Q2 结果(${r.q2.attrs.join(", ")}):`);
  if (r.q2.rows.length === 0) lines.push("  <空>");
  for (const row of r.q2.rows) lines.push(`  ${formatTuple(row)}`);
  lines.push("");
  lines.push("差异行:");
  lines.push(
    "  仅在 Q1: " + (r.onlyInQ1.length ? r.onlyInQ1.map(formatTuple).join(" ") : "无"),
  );
  lines.push(
    "  仅在 Q2: " + (r.onlyInQ2.length ? r.onlyInQ2.map(formatTuple).join(" ") : "无"),
  );
  if (r.differences.length > 0) {
    lines.push("");
    lines.push("导致差异的来源行(翻转该行会改变差异行的成员资格):");
    for (const d of r.differences) {
      lines.push(`  差异行 ${formatTuple(d.tuple)} [仅在 ${d.side}]:`);
      if (d.sourceRows.length === 0) {
        lines.push("    (无)");
      }
      for (const s of d.sourceRows) {
        const affects = [s.affectsQ1 ? "Q1" : null, s.affectsQ2 ? "Q2" : null]
          .filter(Boolean)
          .join("、");
        const where = s.inDatabase ? "在数据库中" : "不在数据库中(若加入)";
        lines.push(`    ${s.rel}${formatTuple(s.tuple)}  ${where},影响 ${affects}`);
      }
    }
  }
  return lines.join("\n");
}

function renderHuman(result: SearchResult): string {
  if (result.kind === "different") return renderDifferent(result);
  const lines = [
    `在给定界内等价: 未找到使两查询结果不同的数据库`,
    `  最大总行数: ${result.maxRows}`,
    `  检查数据库数: ${result.databasesChecked}`,
  ];
  if (result.exhaustive) {
    lines.push("  已穷举该属性域下所有可能的数据库,等价性在该域内无条件成立。");
  } else {
    lines.push("  注意: 仅保证总行数不超过上界的等价性。");
  }
  return lines.join("\n");
}

function main(): void {
  const args = process.argv.slice(2);
  let specPath: string | undefined;
  let json = false;
  let maxRows: number | undefined;
  let maxDatabases: number | undefined;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--json") json = true;
    else if (a === "--max-rows") maxRows = Number.parseInt(args[++i] ?? "", 10);
    else if (a === "--max-databases") maxDatabases = Number.parseInt(args[++i] ?? "", 10);
    else if (a === "-h" || a === "--help") usage(0);
    else if (a !== undefined && !a.startsWith("-") && specPath === undefined) specPath = a;
    else {
      console.error(`未知参数: ${a ?? ""}`);
      usage(2);
    }
  }
  let spec: Spec;
  try {
    const text = specPath ? readFileSync(specPath, "utf8") : readFileSync(0, "utf8");
    spec = JSON.parse(text) as Spec;
  } catch (e) {
    console.error(`读取/解析 spec 失败: ${(e as Error).message}`);
    process.exit(2);
  }
  if (maxRows !== undefined) {
    if (!Number.isInteger(maxRows) || maxRows < 0) {
      console.error("--max-rows 必须是非负整数");
      process.exit(2);
    }
    spec.maxRows = maxRows;
  }
  try {
    const result = findDifference(spec, { maxDatabases });
    console.log(json ? JSON.stringify(result, null, 2) : renderHuman(result));
    process.exitCode = result.kind === "different" ? 1 : 0;
  } catch (e) {
    if (e instanceof QueryError) {
      console.error(`错误: ${e.message}`);
    } else {
      console.error(`内部错误: ${(e as Error).stack ?? e}`);
    }
    process.exit(2);
  }
}

main();
