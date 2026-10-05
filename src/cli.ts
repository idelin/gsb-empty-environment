import { readFileSync, writeFileSync } from "fs";
import { migrateCoverage } from "./migrate";
import { formatRange } from "./ranges";
import {
  MappingRule,
  MigrationReport,
  NewCoverageModel,
  OldCoverageData,
} from "./types";

interface CliOptions {
  oldPath: string;
  newPath: string;
  rulesPath: string;
  outPath: string;
  format: string;
}

const USAGE = [
  "Usage: coverage-migrate --old <old.json> --new <new.json> [--rules <rules.json>]",
  "                        [--out <report.json>] [--format json|text]",
  "",
  "Migrates statement coverage from an old data snapshot onto a new statement",
  "model. Only statements with a proven correspondence inherit coverage;",
  "everything else is reported as added (new side) or uncertain (old side).",
].join("\n");

function parseArgs(argv: string[]): CliOptions {
  const opts: CliOptions = {
    oldPath: "",
    newPath: "",
    rulesPath: "",
    outPath: "",
    format: "text",
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const next = argv[i + 1];
    if (arg === "--old" && next) {
      opts.oldPath = next;
      i++;
    } else if (arg === "--new" && next) {
      opts.newPath = next;
      i++;
    } else if (arg === "--rules" && next) {
      opts.rulesPath = next;
      i++;
    } else if (arg === "--out" && next) {
      opts.outPath = next;
      i++;
    } else if (arg === "--format" && next) {
      opts.format = next;
      i++;
    } else if (arg === "--help" || arg === "-h") {
      console.log(USAGE);
      process.exit(0);
    } else {
      console.error("unknown argument: " + arg);
      console.error(USAGE);
      process.exit(2);
    }
  }
  if (!opts.oldPath || !opts.newPath) {
    console.error("error: --old and --new are required");
    console.error(USAGE);
    process.exit(2);
  }
  return opts;
}

function readJson(path: string): unknown {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function formatReport(report: MigrationReport): string {
  const lines: string[] = [];
  lines.push(
    "Summary: " +
      report.summary.inherited +
      " inherited, " +
      report.summary.added +
      " added, " +
      report.summary.uncertain +
      " uncertain " +
      "(old=" +
      report.summary.oldTotal +
      ", new=" +
      report.summary.newTotal +
      ")"
  );
  lines.push("");
  lines.push("Inherited (" + report.inherited.length + "):");
  for (const item of report.inherited) {
    const src = item.sources
      .map(function (s) {
        return s.oldId + "(hits=" + s.hits + ")";
      })
      .join(", ");
    lines.push(
      "  " +
        item.newId +
        "  " +
        item.file +
        "@" +
        formatRange(item.range) +
        "  <= " +
        src +
        "  [" +
        item.provenance.join(", ") +
        "]"
    );
  }
  lines.push("");
  lines.push("Added (" + report.added.length + "):");
  for (const item of report.added) {
    lines.push(
      "  " + item.newId + "  " + item.file + "@" + formatRange(item.range)
    );
  }
  lines.push("");
  lines.push("Uncertain (" + report.uncertain.length + "):");
  for (const item of report.uncertain) {
    let line =
      "  " +
      item.oldId +
      "  " +
      item.file +
      "@" +
      formatRange(item.range) +
      "  hits=" +
      item.hits +
      "  -- " +
      item.reason;
    if (item.candidates.length > 0) {
      line += " (candidates: " + item.candidates.join(", ") + ")";
    }
    lines.push(line);
  }
  if (report.diagnostics.length > 0) {
    lines.push("");
    lines.push("Diagnostics (" + report.diagnostics.length + "):");
    for (const d of report.diagnostics) {
      lines.push("  " + d);
    }
  }
  return lines.join("\n");
}

function main(): void {
  const opts = parseArgs(process.argv.slice(2));
  const oldData = readJson(opts.oldPath) as OldCoverageData;
  const newData = readJson(opts.newPath) as NewCoverageModel;
  let rules: MappingRule[] = [];
  if (opts.rulesPath) {
    const parsed = readJson(opts.rulesPath) as { rules: MappingRule[] };
    rules = parsed.rules || [];
  }
  const report = migrateCoverage(oldData, newData, rules);
  const output =
    opts.format === "json" ? JSON.stringify(report, null, 2) : formatReport(report);
  if (opts.outPath) {
    writeFileSync(opts.outPath, output + "\n", "utf8");
    console.log("report written to " + opts.outPath);
  } else {
    console.log(output);
  }
  if (report.uncertain.length > 0) {
    process.exitCode = 1;
  }
}

main();
