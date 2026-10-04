import { AuditReport, Counterexample } from "./auditor";
import { Environment, InputValue } from "./model";

function formatValue(v: InputValue | undefined): string {
  return v === undefined ? "<undeclared>" : JSON.stringify(v);
}

function formatEnv(env: Environment, only: readonly string[]): string {
  return only.map((n) => `${n}=${formatValue(env[n])}`).join("  ");
}

function formatCounterexample(ce: Counterexample, index: number, groupSize: number): string {
  const lines: string[] = [];
  const extra = groupSize > 1 ? ` — representative of ${groupSize} keys` : "";
  lines.push(
    `  [${index}] ${ce.kind} via ${ce.diffInputs.join(", ")}${extra}`,
  );
  const keyEntries = Object.entries(ce.key)
    .map(([k, v]) => `${k}=${formatValue(v)}`)
    .join("  ");
  lines.push(`      shared key:   ${keyEntries.length > 0 ? keyEntries : "(empty key)"}`);
  lines.push(`      env A:        ${formatEnv(ce.a, ce.diffInputs)}`);
  lines.push(`      env B:        ${formatEnv(ce.b, ce.diffInputs)}`);
  if (ce.outputA.artifact !== ce.outputB.artifact) {
    lines.push(`      artifact A:   ${ce.outputA.artifact}`);
    lines.push(`      artifact B:   ${ce.outputB.artifact}`);
  }
  if (ce.outputA.log !== ce.outputB.log) {
    lines.push(`      log A:        ${ce.outputA.log}`);
    lines.push(`      log B:        ${ce.outputB.log}`);
  }
  return lines.join("\n");
}

export function formatReport(report: AuditReport): string {
  const lines: string[] = [];
  lines.push(`Cache-key audit: ${report.specName}`);
  lines.push(
    `  enumerated ${report.environmentCount} environments, ${report.keyCount} distinct cache keys`,
  );
  lines.push("");

  const artifactCEs = report.counterexamples.filter((c) => c.kind === "artifact-affecting");
  const logCEs = report.counterexamples.filter((c) => c.kind === "log-only");

  if (report.counterexamples.length === 0) {
    lines.push("RESULT: SOUND — every output-changing input is reflected in the key.");
  } else {
    lines.push(
      `RESULT: UNSOUND — ${artifactCEs.length} artifact-affecting, ${logCEs.length} log-only minimal counterexample(s).`,
    );
  }

  const printGroup = (title: string, ces: Counterexample[]): void => {
    if (ces.length === 0) return;
    lines.push("");
    lines.push(title);
    const byDiff = new Map<string, Counterexample[]>();
    for (const ce of ces) {
      const id = ce.diffInputs.join(",");
      const bucket = byDiff.get(id);
      if (bucket) bucket.push(ce);
      else byDiff.set(id, [ce]);
    }
    let index = 0;
    for (const bucket of byDiff.values()) {
      index++;
      lines.push(formatCounterexample(bucket[0]!, index, bucket.length));
    }
  };
  printGroup(
    "Artifact-affecting counterexamples (output changes, key does not):",
    artifactCEs,
  );
  printGroup("Log-only counterexamples (only diagnostics change):", logCEs);

  lines.push("");
  lines.push("Input effects:");
  const keySet = new Set(report.keyInputs);
  for (const [name, effect] of Object.entries(report.inputEffects)) {
    const tag = keySet.has(name) ? "  [in key]" : "";
    lines.push(`  ${effect.padEnd(19)} ${name}${tag}`);
  }

  if (report.undeclaredEffectiveInputs.length > 0) {
    lines.push("");
    lines.push(
      `Missing from key (used and output-affecting): ${report.undeclaredEffectiveInputs.join(", ")}`,
    );
  }
  if (report.unusedKeyInputs.length > 0) {
    lines.push(`Declared but ineffective (safe to drop):   ${report.unusedKeyInputs.join(", ")}`);
  }
  if (report.neverReadInputs.length > 0) {
    lines.push(`Never read by the build:                     ${report.neverReadInputs.join(", ")}`);
  }
  return lines.join("\n");
}
