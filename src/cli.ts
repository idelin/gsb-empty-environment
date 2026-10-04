#!/usr/bin/env node
import * as fs from "fs";
import * as path from "path";
import { audit } from "./auditor";
import { loadJsonFile, loadManifest, normalizeDeclarations } from "./manifest";
import { normalizeAbs } from "./fspaths";
import { runWorkload } from "./tracer";
import { AccessLog, AuditReport, CasePolicy, Manifest, Workload } from "./types";

const USAGE = `access-audit — audit declared file access against actual tool access

Usage:
  access-audit run    --workload ops.json [--base DIR] [--out access-log.json]
  access-audit audit  --manifest decl.json --log access-log.json
                      [--case sensitive|insensitive|auto]
                      [--report report.json] [--supplement-out supplement.json]
  access-audit check  --manifest decl.json --workload ops.json
                      [--base DIR] [--case sensitive|insensitive|auto]
                      [--report report.json] [--supplement-out supplement.json]

Exit code is 0 when every actual access is declared, 1 otherwise.
`;

interface ParsedArgs {
  command: string;
  flags: Map<string, string>;
}

function parseArgs(argv: string[]): ParsedArgs {
  const [command, ...rest] = argv;
  const flags = new Map<string, string>();
  for (let i = 0; i < rest.length; i += 1) {
    const arg = rest[i];
    if (!arg.startsWith("--")) {
      throw new Error(`unexpected argument: ${arg}`);
    }
    const eq = arg.indexOf("=");
    if (eq >= 0) {
      flags.set(arg.slice(2, eq), arg.slice(eq + 1));
    } else {
      const value = rest[i + 1];
      if (value === undefined || value.startsWith("--")) {
        throw new Error(`missing value for ${arg}`);
      }
      flags.set(arg.slice(2), value);
      i += 1;
    }
  }
  if (!command) throw new Error("missing command");
  return { command, flags };
}

function required(flags: Map<string, string>, name: string): string {
  const value = flags.get(name);
  if (!value) throw new Error(`missing required flag --${name}`);
  return value;
}

function writeJson(file: string | undefined, data: unknown): void {
  const text = JSON.stringify(data, null, 2) + "\n";
  if (file) {
    fs.writeFileSync(file, text);
  } else {
    process.stdout.write(text);
  }
}

function resolveCaseFlag(
  flags: Map<string, string>,
  manifest?: Manifest
): CasePolicy {
  const raw = flags.get("case") ?? manifest?.case ?? "auto";
  if (raw !== "sensitive" && raw !== "insensitive" && raw !== "auto") {
    throw new Error(`invalid --case policy: ${raw}`);
  }
  return raw;
}

function printSummary(report: AuditReport): void {
  const s = report.summary;
  const lines = [
    `case policy : ${report.casePolicy}`,
    `accesses    : ${s.totalAccesses} total, ${s.uniqueAccesses} unique`,
    `declared    : ${s.declared}`,
    `undeclared  : ${s.undeclared}`,
    `escapes     : ${s.symlinkEscapes}`,
    `missing     : ${s.missing}`,
  ];
  process.stderr.write(lines.join("\n") + "\n");
  for (const a of report.accesses) {
    if (a.status === "declared") continue;
    const dup = a.count > 1 ? ` (x${a.count})` : "";
    process.stderr.write(`  [${a.status}] ${a.op} ${a.path}${dup}\n`);
    if (a.reason) process.stderr.write(`      reason: ${a.reason}\n`);
    for (const hop of a.chain) {
      process.stderr.write(`      link: ${hop.at} -> ${hop.resolved}\n`);
    }
  }
  if (report.supplement.length > 0) {
    process.stderr.write(
      `supplement  : ${report.supplement.length} entr${
        report.supplement.length === 1 ? "y" : "ies"
      } needed\n`
    );
  }
}

function doRun(flags: Map<string, string>): number {
  const workload = loadJsonFile<Workload>(required(flags, "workload"));
  const base = path.resolve(flags.get("base") ?? process.cwd());
  const log = runWorkload(workload, base);
  writeJson(flags.get("out"), log);
  return 0;
}

function loadDeclarations(flags: Map<string, string>, base: string) {
  const manifest = loadManifest(required(flags, "manifest"));
  const declarations = normalizeDeclarations(
    manifest.declarations,
    base,
    normalizeAbs
  );
  return { manifest, declarations };
}

function emitReport(
  flags: Map<string, string>,
  report: AuditReport
): number {
  writeJson(flags.get("report"), report);
  const supplementOut = flags.get("supplement-out");
  if (supplementOut) {
    writeJson(supplementOut, {
      version: 1,
      declarations: report.supplement.map((s) => ({
        path: s.path,
        type: s.type,
        ...(s.recursive !== undefined ? { recursive: s.recursive } : {}),
      })),
    });
  }
  printSummary(report);
  return report.summary.undeclared + report.summary.symlinkEscapes > 0
    ? 1
    : 0;
}

function doAudit(flags: Map<string, string>): number {
  const base = path.resolve(flags.get("base") ?? process.cwd());
  const { manifest, declarations } = loadDeclarations(flags, base);
  const log = loadJsonFile<AccessLog>(required(flags, "log"));
  const report = audit(log, declarations, {
    casePolicy: resolveCaseFlag(flags, manifest),
    probeDir: base,
  });
  return emitReport(flags, report);
}

function doCheck(flags: Map<string, string>): number {
  const base = path.resolve(flags.get("base") ?? process.cwd());
  const { manifest, declarations } = loadDeclarations(flags, base);
  const workload = loadJsonFile<Workload>(required(flags, "workload"));
  const log = runWorkload(workload, base);
  const report = audit(log, declarations, {
    casePolicy: resolveCaseFlag(flags, manifest),
    probeDir: base,
  });
  return emitReport(flags, report);
}

function main(): number {
  const { command, flags } = parseArgs(process.argv.slice(2));
  switch (command) {
    case "run":
      return doRun(flags);
    case "audit":
      return doAudit(flags);
    case "check":
      return doCheck(flags);
    case "help":
    case "--help":
    case "-h":
      process.stdout.write(USAGE);
      return 0;
    default:
      throw new Error(`unknown command: ${command}`);
  }
}

try {
  process.exitCode = main();
} catch (err) {
  process.stderr.write(`access-audit: ${(err as Error).message}\n\n${USAGE}`);
  process.exitCode = 2;
}
