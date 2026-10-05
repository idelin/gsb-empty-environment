import { readFileSync } from "node:fs";
import { analyze, type AnalysisReport } from "./analyze.ts";
import { compileTable, type CompiledTable, type TableJson } from "./model.ts";
import { formatCount, renderAssignment, renderBox } from "./render.ts";

function spaceSize(table: CompiledTable): number {
  let acc = 1;
  for (const s of table.domainSizes) acc = Math.min(Number.MAX_SAFE_INTEGER, acc * s);
  return acc;
}

function toJsonReport(table: CompiledTable, report: AnalysisReport): unknown {
  return {
    fields: table.fields.map((f) => f.name),
    inputSpaceSize: formatCount(spaceSize(table)),
    rows: report.rows.map((row) => ({
      rule: row.rule,
      priority: row.priority,
      status: row.status,
      witness: row.witness,
      tiePartners: row.tiePartners,
      vacuousFields: row.vacuousFields,
      cover: row.cover?.map((step) => ({
        rule: step.rule,
        cells: step.cells.map((cell) => renderBox(table.fields, cell)),
      })),
    })),
    conflicts: report.conflicts,
  };
}

function printTextReport(table: CompiledTable, report: AnalysisReport): void {
  const lines: string[] = [];
  lines.push(
    `Decision table analysis: ${table.rules.length} rules, ${table.fields.length} fields, ` +
      `${formatCount(spaceSize(table))} possible inputs`,
  );
  lines.push("");
  for (const row of report.rows) {
    const head = `${row.rule} (priority ${row.priority})`;
    if (row.status === "wins") {
      lines.push(`WINS     ${head}`);
      lines.push(`         minimal winning input: ${renderAssignment(row.witness!)}`);
    } else if (row.status === "ties") {
      lines.push(`TIES     ${head}`);
      lines.push(`         reaches top priority but never alone, e.g. ${renderAssignment(row.witness!)}`);
      lines.push(`         always tied with: ${row.tiePartners!.join(", ") || "(none)"}`);
    } else if (row.status === "covered") {
      lines.push(`COVERED  ${head}`);
      lines.push(`         never wins; every matching input is captured by higher-priority rules:`);
      for (const step of row.cover!) {
        lines.push(`           - ${step.rule} covers:`);
        for (const cell of step.cells) lines.push(`               ${renderBox(table.fields, cell)}`);
      }
    } else {
      lines.push(`VACUOUS  ${head}`);
      lines.push(`         unsatisfiable conditions on: ${row.vacuousFields!.join(", ")}`);
    }
    lines.push("");
  }
  if (report.conflicts.length > 0) {
    lines.push("Same-priority conflicts (an input hitting both rules):");
    for (const c of report.conflicts) {
      const masked = c.masked ? " [masked: a higher-priority rule always fires there too]" : "";
      lines.push(`  ${c.rules[0]}  x  ${c.rules[1]}  (priority ${c.priority})${masked}`);
      lines.push(`    ${renderAssignment(c.witness)}`);
    }
  } else {
    lines.push("No same-priority conflicts.");
  }
  process.stdout.write(lines.join("\n") + "\n");
}

function main(): void {
  const args = process.argv.slice(2);
  const jsonOut = args.includes("--json");
  const file = args.find((a) => !a.startsWith("--"));
  if (!file) {
    process.stderr.write("usage: node src/cli.ts [--json] <table.json>\n");
    process.exit(2);
  }
  const raw = JSON.parse(readFileSync(file, "utf8")) as TableJson;
  const table = compileTable(raw);
  const report = analyze(table);
  if (jsonOut) {
    process.stdout.write(JSON.stringify(toJsonReport(table, report), null, 2) + "\n");
  } else {
    printTextReport(table, report);
  }
}

main();
