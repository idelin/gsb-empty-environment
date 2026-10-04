import fs from "node:fs";
import path from "node:path";
import { audit } from "./auditor.ts";
import { loadManifest, loadTrace, prepareDeclarations } from "./manifest.ts";
import { resolveCasePolicy } from "./paths.ts";
import type { AuditReport, CasePolicy } from "./types.ts";

const USAGE = `Usage: node src/cli.ts audit --manifest <declared.json> --trace <accesses.json> [options]

Options:
  --case <sensitive|insensitive|auto>  Path case policy (default: manifest's
                                       casePolicy, else "auto").
  --base <dir>                         Base directory for relative paths
                                       (default: manifest's directory).
  --report <file>                      Write the full JSON audit report.
  --supplement <file>                  Write the minimal supplemental manifest.
  -h, --help                           Show this help.
`;

interface CliArgs {
  manifest?: string;
  trace?: string;
  casePolicy?: CasePolicy;
  base?: string;
  report?: string;
  supplement?: string;
}

function parseArgs(argv: string[]): CliArgs {
  const args: CliArgs = {};
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    const value = (): string => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`missing value for ${flag}`);
      return v;
    };
    switch (flag) {
      case "--manifest": args.manifest = value(); break;
      case "--trace": args.trace = value(); break;
      case "--case": {
        const v = value();
        if (v !== "sensitive" && v !== "insensitive" && v !== "auto") {
          throw new Error(`invalid --case policy "${v}"`);
        }
        args.casePolicy = v;
        break;
      }
      case "--base": args.base = value(); break;
      case "--report": args.report = value(); break;
      case "--supplement": args.supplement = value(); break;
      default: throw new Error(`unknown argument: ${flag}`);
    }
  }
  return args;
}

function printSummary(report: AuditReport): void {
  const s = report.summary;
  console.log(`case policy:        ${report.casePolicy}`);
  console.log(`trace events:       ${s.totalEvents} (${s.duplicateEvents} duplicates folded)`);
  console.log(`unique accesses:    ${s.uniqueAccesses}`);
  console.log(`declared:           ${s.declared}`);
  console.log(`undeclared:         ${s.undeclared}`);
  console.log(`unverifiable:       ${s.unverifiable}`);
  console.log(`missing paths:      ${s.missingPaths}`);

  if (report.undeclared.length > 0) {
    console.log("\nUndeclared accesses:");
    for (const record of report.undeclared) {
      console.log(`  [${record.status}] ${record.path} (${record.kinds.join(",")}, x${record.count})`);
      if (record.resolved.hops.length > 0) {
        for (const hop of record.resolved.hops) {
          console.log(`      symlink: ${hop.at} -> ${hop.target}`);
        }
        console.log(`      resolves to: ${record.resolved.canonical}`);
      }
      if (record.reason) console.log(`      reason: ${record.reason}`);
    }
  }

  const sup = report.supplement;
  const total = sup.files.length + sup.directories.length + sup.executables.length;
  if (total > 0) {
    console.log("\nMinimal supplemental manifest:");
    for (const d of sup.directories) console.log(`  directory:  ${d}`);
    for (const f of sup.files) console.log(`  file:       ${f}`);
    for (const e of sup.executables) console.log(`  executable: ${e}`);
  }
}

function main(argv: string[]): number {
  const [command, ...rest] = argv;
  if (command === "-h" || command === "--help" || command === undefined) {
    process.stdout.write(USAGE);
    return command === undefined ? 1 : 0;
  }
  if (command !== "audit") {
    throw new Error(`unknown command "${command}"\n\n${USAGE}`);
  }

  const args = parseArgs(rest);
  if (!args.manifest) throw new Error("--manifest is required");
  if (!args.trace) throw new Error("--trace is required");

  const manifestPath = path.resolve(args.manifest);
  const manifest = loadManifest(manifestPath);
  const events = loadTrace(path.resolve(args.trace));
  const base = path.resolve(args.base ?? path.dirname(manifestPath));

  const policy = resolveCasePolicy(args.casePolicy ?? manifest.casePolicy, base);
  const decls = prepareDeclarations(manifest, base);
  const report = audit(events, decls, { policy, cwd: base });

  if (args.report) {
    fs.writeFileSync(args.report, JSON.stringify(report, null, 2) + "\n");
    console.log(`report written to ${args.report}`);
  }
  if (args.supplement) {
    fs.writeFileSync(args.supplement, JSON.stringify(report.supplement, null, 2) + "\n");
    console.log(`supplement written to ${args.supplement}`);
  }

  printSummary(report);
  return report.undeclared.length > 0 ? 2 : 0;
}

try {
  process.exitCode = main(process.argv.slice(2));
} catch (err) {
  console.error(`error: ${err instanceof Error ? err.message : String(err)}`);
  process.exitCode = 1;
}
