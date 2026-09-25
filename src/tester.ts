/**
 * Equivalence tester.
 *
 * Given two expressions and a finite schema it verifies:
 *
 *  1. VALUE EQUIVALENCE — for every assignment in the (finite) domain,
 *     including every attribute-absent assignment, the two expressions produce
 *     the identical strong-Kleene value TRUE / FALSE / MISSING.
 *  2. PROVENANCE SOUNDNESS — no source id appears in the simplified tree (or
 *     in a reported lineage entry) that was not present in the original tree.
 *     Simplification may drop or merge sources, never invent them.
 *
 * It works for ANY transform, so it can also reject tempting-but-unsound rules,
 * notably turning `x OR NOT x` into `true` on a nullable domain.
 */
import type { Node } from "./ast.ts";
import { evaluate } from "./eval.ts";
import { allSources, simplify } from "./simplify.ts";
import type { LineageEntry } from "./simplify.ts";
import {
  domainSize,
  enumerateAssignments,
  validateSchema,
} from "./schema.ts";
import type { Assignment, Schema } from "./schema.ts";
import { triName } from "./trivalue.ts";
import type { TriValue } from "./trivalue.ts";

export interface Mismatch {
  assignment: Assignment;
  before: TriValue;
  after: TriValue;
}

export interface EquivalenceReport {
  equivalent: boolean;
  domainSize: bigint;
  checked: number;
  mismatches: Mismatch[];
  inventedSources: string[];
  lineage: LineageEntry[];
}

export interface TransformReport extends Omit<EquivalenceReport, "lineage"> {
  lineage?: LineageEntry[];
}

function sourceLeak(before: Node, after: Node, lineage?: LineageEntry[]): string[] {
  const allowed = allSources(before);
  const found = allSources(after);
  const invented = new Set<string>();
  for (const s of found) if (!allowed.has(s)) invented.add(s);
  if (lineage) {
    for (const entry of lineage) for (const s of entry.sources) if (!allowed.has(s)) invented.add(s);
  }
  return [...invented].sort();
}

/** Exhaustive three-valued + provenance comparison of two concrete trees. */
export function checkEquivalence(
  before: Node,
  after: Node,
  schema: Schema,
  lineage?: LineageEntry[],
  options: { maxMismatches?: number } = {},
): EquivalenceReport {
  validateSchema(schema);
  const maxMismatches = options.maxMismatches ?? 10;
  const mismatches: Mismatch[] = [];
  let checked = 0;
  for (const assignment of enumerateAssignments(schema)) {
    checked += 1;
    const a = evaluate(before, assignment, schema);
    const b = evaluate(after, assignment, schema);
    if (a !== b && mismatches.length < maxMismatches) {
      mismatches.push({ assignment, before: a, after: b });
    }
  }
  const inventedSources = sourceLeak(before, after, lineage);
  return {
    equivalent: mismatches.length === 0 && inventedSources.length === 0,
    domainSize: domainSize(schema),
    checked,
    mismatches,
    inventedSources,
    lineage: lineage ?? [],
  };
}

/** Run the shipped simplifier and verify it exhaustively. */
export function testSimplification(
  expr: Node,
  schema: Schema,
): EquivalenceReport {
  const { expr: simplified, lineage } = simplify(expr, schema);
  return checkEquivalence(expr, simplified, schema, lineage);
}

/**
 * Check an arbitrary rewrite rule/transform for soundness.
 * The transform may return null to decline a case.
 */
export function checkTransform(
  build: (schema: Schema) => Node,
  transform: (expr: Node, schema: Schema) => Node | null,
  schema: Schema,
): TransformReport & { declined: boolean } {
  const expr = build(schema);
  const transformed = transform(expr, schema);
  if (!transformed) {
    return {
      equivalent: true,
      domainSize: domainSize(schema),
      checked: 0,
      mismatches: [],
      inventedSources: [],
      declined: true,
    };
  }
  const report = checkEquivalence(expr, transformed, schema);
  return { ...report, declined: false };
}

export function formatReport(report: EquivalenceReport): string {
  const lines: string[] = [];
  lines.push(
    `${report.equivalent ? "EQUIVALENT" : "NOT EQUIVALENT"} — checked ${report.checked}/${report.domainSize} finite-domain inputs`,
  );
  for (const m of report.mismatches) {
    const keys = Object.keys(m.assignment);
    const desc = keys.length === 0 ? "all attributes missing" : JSON.stringify(m.assignment);
    lines.push(`  mismatch @ ${desc}: before=${triName(m.before)} after=${triName(m.after)}`);
  }
  if (report.inventedSources.length > 0) {
    lines.push(`  invented sources: ${report.inventedSources.join(", ")}`);
  }
  if (report.lineage.length > 0) {
    lines.push("  lineage:");
    for (const entry of report.lineage) {
      lines.push(`    - ${entry.rule}: ${entry.detail} <- {${entry.sources.join(",")}}`);
    }
  }
  return lines.join("\n");
}
