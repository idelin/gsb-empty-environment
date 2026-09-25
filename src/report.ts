import { describeScope } from "./domain.ts";
import type { CompareResult, Region } from "./compare.ts";
import type { DomainSpec, Input } from "./types.ts";

function fmtInput(input: Input): string {
  return Object.entries(input)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : String(v)}`)
    .join(", ");
}

function fmtRules(ids: readonly string[], defaulted: boolean): string {
  if (defaulted) return "(default effect, no rule matched)";
  return ids.length > 0 ? `rules [${ids.join(", ")}]` : "(none)";
}

function fmtRegion(r: Region): string[] {
  const lines: string[] = [];
  if (r.changeType === "decision-changed") {
    lines.push(`  ${r.direction}  points=${r.count}`);
    lines.push(`    representative input: { ${fmtInput(r.sample)} }`);
    lines.push(
      `    old: ${r.oldEffect} via ${fmtRules(r.oldDecision.matchedRuleIds, r.oldDecision.defaulted)}`,
    );
    lines.push(
      `    new: ${r.newEffect} via ${fmtRules(r.newDecision.matchedRuleIds, r.newDecision.defaulted)}`,
    );
  } else {
    lines.push(
      `  explanation-only (effect stays "${r.oldEffect}")  points=${r.count}`,
    );
    lines.push(`    representative input: { ${fmtInput(r.sample)} }`);
    lines.push(
      `    old explanation: ${fmtRules(r.oldDecision.matchedRuleIds, r.oldDecision.defaulted)}`,
    );
    lines.push(
      `    new explanation: ${fmtRules(r.newDecision.matchedRuleIds, r.newDecision.defaulted)}`,
    );
  }
  const projected = r.projections
    .filter((p) => !p.spansAll)
    .map((p) => `${p.attr} ∈ {${p.values.join(", ")}}`);
  if (projected.length > 0) lines.push(`    region: ${projected.join("; ")}`);
  return lines;
}

export function renderReport(
  result: CompareResult,
  domain: DomainSpec,
  oldName: string,
  newName: string,
): string {
  const out: string[] = [];
  out.push("=== Semantic rule-set comparison ===");
  out.push(`old set: ${oldName}   new set: ${newName}`);
  out.push(
    "evaluation: order-independent deny-overrides; rules read explicit attributes only",
  );
  out.push(
    `finite analysis scope (${result.totalInputs} input points): ${describeScope(domain)}`,
  );
  out.push("");

  const s = result.summary;
  out.push("--- Summary (counts of input points) ---");
  out.push(`  decision changed  allow -> deny : ${s.allowToDeny}`);
  out.push(`  decision changed  deny  -> allow: ${s.denyToAllow}`);
  out.push(`  explanation only (effect unchanged): ${s.explanationOnly}`);
  out.push(`  unchanged                         : ${s.unchanged}`);
  out.push("");

  const allowToDeny = result.regions.filter((r) => r.direction === "allow->deny");
  const denyToAllow = result.regions.filter((r) => r.direction === "deny->allow");
  const explain = result.regions.filter((r) => r.changeType === "explanation-only");

  out.push(`### allow -> deny regions (${allowToDeny.length})`);
  for (const r of allowToDeny) out.push(...fmtRegion(r));
  out.push("");
  out.push(`### deny -> allow regions (${denyToAllow.length})`);
  for (const r of denyToAllow) out.push(...fmtRegion(r));
  out.push("");
  out.push(`### explanation-only regions (${explain.length})`);
  for (const r of explain) out.push(...fmtRegion(r));
  out.push("");

  const unchangedCount = result.regions.filter(
    (r) => r.changeType === "unchanged",
  ).length;
  out.push(
    `### unchanged regions: ${unchangedCount} (${s.unchanged} points, details omitted)`,
  );
  return out.join("\n");
}
