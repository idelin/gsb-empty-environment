import { applyAlias, canonicalKeyOfNormalized, dirSegmentsOf, hasKnownExtension, literalKey, literalSegments, normalizeSegments, segmentsStartWith, splitSegments, stripKnownExtension } from "./canonicalize.js";
import type { AliasRule, CanonicalRules } from "./canonicalize.js";
import type { KeyedModule } from "./conflicts.js";
import type { RenamePlan } from "./rename.js";
import type { ReferenceEntry, ReferenceReport } from "./types.js";

export type RefKind = "relative" | "alias" | "external";

export interface ResolvedRef {
  kind: RefKind;
  segs: string[];
  alias: AliasRule | null;
}

export function resolveSpecifier(specifier: string, fromPath: string, rules: CanonicalRules): ResolvedRef {
  const spec = specifier.split("\\").join("/");
  const isRelative = spec === "." || spec === ".." || spec.startsWith("./") || spec.startsWith("../");
  const raw = splitSegments(spec);
  if (isRelative) {
    const base = dirSegmentsOf(fromPath, rules);
    return { kind: "relative", segs: normalizeSegments(base.concat(raw)), alias: null };
  }
  const aliased = applyAlias(raw, rules);
  if (aliased.alias !== null) {
    return { kind: "alias", segs: normalizeSegments(aliased.segs), alias: aliased.alias };
  }
  return { kind: "external", segs: [], alias: null };
}

function relativeSegments(fromDir: string[], target: string[]): string[] {
  let common = 0;
  while (common < fromDir.length && common < target.length && fromDir[common] === target[common]) {
    common++;
  }
  const out: string[] = [];
  for (let i = common; i < fromDir.length; i++) {
    out.push("..");
  }
  return out.concat(target.slice(common));
}

function lastSegmentOf(specifier: string): string {
  const segs = splitSegments(specifier);
  if (segs.length === 0) {
    return "";
  }
  return segs[segs.length - 1];
}

function formatTargetSegments(segsIn: string[], keepExt: boolean, keepIndex: boolean, rules: CanonicalRules): string[] {
  const segs = segsIn.slice();
  if (segs.length === 0) {
    return segs;
  }
  const lastIdx = segs.length - 1;
  if (!keepExt) {
    segs[lastIdx] = stripKnownExtension(segs[lastIdx], rules);
  }
  if (!keepIndex && segs[lastIdx].toLowerCase() === "index") {
    segs.pop();
  }
  return segs;
}

function rebuildViaAlias(targetSegs: string[], rules: CanonicalRules): string[] | null {
  for (const alias of rules.aliasesInverse) {
    if (segmentsStartWith(targetSegs, alias.to)) {
      return alias.from.concat(targetSegs.slice(alias.to.length));
    }
  }
  return null;
}

export function rewriteSpecifier(original: string, resolved: ResolvedRef, newFrom: string, newTarget: string, rules: CanonicalRules): string {
  const keepExt = hasKnownExtension(lastSegmentOf(original), rules);
  const stem = stripKnownExtension(lastSegmentOf(original), rules);
  const keepIndex = stem.toLowerCase() === "index";
  const targetSegs = literalSegments(newTarget, rules);
  if (resolved.kind === "alias") {
    const rebuilt = rebuildViaAlias(targetSegs, rules);
    if (rebuilt !== null) {
      const formatted = formatTargetSegments(rebuilt, keepExt, keepIndex, rules);
      if (formatted.length === 0) {
        return ".";
      }
      return formatted.join("/");
    }
  }
  const fromDir = dirSegmentsOf(newFrom, rules);
  const rel = relativeSegments(fromDir, targetSegs);
  const formatted = formatTargetSegments(rel, keepExt, keepIndex, rules);
  if (formatted.length === 0) {
    return ".";
  }
  if (formatted[0] !== "..") {
    return "./" + formatted.join("/");
  }
  return formatted.join("/");
}
export function buildReferenceReports(references: ReferenceEntry[], modules: KeyedModule[], plan: RenamePlan, rules: CanonicalRules): ReferenceReport[] {
  const exactByLiteral: Map<string, string> = new Map();
  const pathsByKey: Map<string, string[]> = new Map();
  for (const m of modules) {
    exactByLiteral.set(literalKey(m.path, rules), m.path);
    const list = pathsByKey.get(m.key);
    if (list) {
      list.push(m.path);
    } else {
      pathsByKey.set(m.key, [m.path]);
    }
  }
  const reports: ReferenceReport[] = [];
  for (const ref of references) {
    reports.push(buildOne(ref, exactByLiteral, pathsByKey, plan, rules));
  }
  return reports;
}

function buildOne(ref: ReferenceEntry, exactByLiteral: Map<string, string>, pathsByKey: Map<string, string[]>, plan: RenamePlan, rules: CanonicalRules): ReferenceReport {
  const newFrom = plan.finalPathOf.get(ref.from) ?? ref.from;
  const report: ReferenceReport = {
    from: ref.from,
    newFrom,
    specifier: ref.specifier,
    newSpecifier: null,
    resolvesTo: null,
    candidates: [],
    status: "unresolved",
  };
  const resolved = resolveSpecifier(ref.specifier, ref.from, rules);
  if (resolved.kind === "external") {
    report.status = "external";
    return report;
  }
  let target = exactByLiteral.get(resolved.segs.join("/")) ?? null;
  if (target === null) {
    const key = canonicalKeyOfNormalized(resolved.segs, rules);
    const prePaths = pathsByKey.get(key);
    if (!prePaths || prePaths.length === 0) {
      report.status = "unresolved";
      return report;
    }
    if (prePaths.length > 1) {
      report.status = "ambiguous";
      report.candidates = prePaths.slice().sort();
      return report;
    }
    target = prePaths[0];
  }
  const finalTarget = plan.finalPathOf.get(target) ?? target;
  report.resolvesTo = finalTarget;
  const moved = finalTarget !== target || newFrom !== ref.from;
  if (moved) {
    const rewritten = rewriteSpecifier(ref.specifier, resolved, newFrom, finalTarget, rules);
    if (rewritten !== ref.specifier) {
      report.newSpecifier = rewritten;
      report.status = "updated";
      return report;
    }
  }
  report.status = "ok";
  return report;
}
