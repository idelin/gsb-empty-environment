import { canonicalKey, literalKey, makeRules } from "./canonicalize.js";
import { findConflicts } from "./conflicts.js";
import type { KeyedModule } from "./conflicts.js";
import { planRenames } from "./rename.js";
import { buildReferenceReports } from "./references.js";
import type { AnalysisReport, BundlerInput, CanonicalKeyEntry } from "./types.js";

function comparePath(a: string, b: string): number {
  if (a < b) {
    return -1;
  }
  if (a > b) {
    return 1;
  }
  return 0;
}

export function analyzeBundle(input: BundlerInput): AnalysisReport {
  const rules = makeRules(input.options);
  const rawModules = input.modules;
  if (!Array.isArray(rawModules) || rawModules.length === 0) {
    throw new Error("input.modules must be a non-empty array");
  }
  const entries: KeyedModule[] = [];
  const dupGroups: Map<string, string[]> = new Map();
  for (const raw of rawModules) {
    let path = "";
    let cost = 1;
    if (typeof raw === "string") {
      path = raw;
    } else {
      path = raw.path;
      cost = raw.renameCost ?? 1;
    }
    if (typeof path !== "string" || path.length === 0) {
      throw new Error("module path must be a non-empty string");
    }
    if (typeof cost !== "number" || !Number.isFinite(cost) || cost < 0) {
      throw new Error("renameCost must be a non-negative finite number for module: " + path);
    }
    const dup = literalKey(path, rules);
    const group = dupGroups.get(dup);
    if (group) {
      group.push(path);
    } else {
      dupGroups.set(dup, [path]);
      entries.push({ path, key: canonicalKey(path, rules), cost });
    }
  }
  const duplicates: string[][] = [];
  for (const paths of dupGroups.values()) {
    if (paths.length > 1) {
      duplicates.push(paths.slice().sort());
    }
  }
  duplicates.sort((a: string[], b: string[]) => comparePath(a[0], b[0]));
  const conflicts = findConflicts(entries);
  const plan = planRenames(entries, conflicts, rules);
  const references = buildReferenceReports(input.references ?? [], entries, plan, rules);
  const canonicalKeys: CanonicalKeyEntry[] = entries.map((m: KeyedModule) => ({ module: m.path, key: m.key }));
  let ok = duplicates.length === 0;
  for (const r of references) {
    if (r.status === "ambiguous" || r.status === "unresolved") {
      ok = false;
    }
  }
  return {
    ok,
    canonicalKeys,
    duplicates,
    conflicts,
    renames: plan.renames,
    totalCost: plan.totalCost,
    references,
  };
}
