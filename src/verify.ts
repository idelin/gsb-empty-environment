import { evalCondition, isViolated, type Assignment } from "./constraints.ts";
import { ExpandError } from "./errors.ts";
import { canonicalize, type JsonValue } from "./json.ts";
import { expand } from "./index.ts";
import { normalizeSpec, type ExpandInput } from "./spec.ts";
import { IdentityExtractor, renderTemplate } from "./template.ts";

export interface VerifyResult {
  ok: boolean;
  streamed: number;
  expected: number;
  /** First rank where the two enumerations disagree, if any. */
  mismatchAt: number | null;
}

/**
 * Naive reference implementation: materializes the FULL Cartesian product,
 * filters by constraints, renders, then dedups by declared identity.
 * Only usable on small domains; this is the ground truth for completeness.
 */
export function bruteForceExpand(input: ExpandInput): JsonValue[] {
  const spec = normalizeSpec(input);
  const out: JsonValue[] = [];
  const seen = new Set<string>();
  const extractor = new IdentityExtractor(spec.identity);
  const assignment: Assignment = new Map();

  function full(cond: import("./spec.ts").Condition): boolean {
    return evalCondition(cond, assignment) === "true";
  }

  function walk(depth: number): void {
    if (depth === spec.axes.length) {
      const violated = spec.constraints.some((c) => {
        if (c.kind === "implies") return full(c.if) && !full(c.then);
        return c.conditions.every(full);
      });
      if (violated) return;
      const record = renderTemplate(spec.template, assignment);
      if (extractor.declared) {
        const key = extractor.keyOf(record);
        if (seen.has(key)) return;
        seen.add(key);
      }
      out.push(record);
      return;
    }
    const axis = spec.axes[depth];
    for (const value of axis.domain) {
      assignment.set(axis.name, value);
      walk(depth + 1);
    }
  }
  walk(0);
  return out;
}

/**
 * Cross-check the streaming engine against the naive full-product expansion.
 * Refuses domains whose raw product exceeds `maxProduct`.
 */
export function verifyCompleteness(
  input: ExpandInput,
  maxProduct = 100_000,
): VerifyResult {
  const spec = normalizeSpec(input);
  const product = spec.axes.reduce((acc, a) => acc * a.domain.length, 1);
  if (product > maxProduct) {
    throw new ExpandError(
      "params",
      "$.axes",
      `domain product ${product} exceeds verification limit ${maxProduct}`,
    );
  }
  const expected = bruteForceExpand(input);
  const streamed = expand(input).records;
  const a = streamed.map(canonicalize);
  const b = expected.map(canonicalize);
  let mismatchAt: number | null = null;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (a[i] !== b[i]) {
      mismatchAt = i;
      break;
    }
  }
  return {
    ok: mismatchAt === null,
    streamed: a.length,
    expected: b.length,
    mismatchAt,
  };
}

// Re-export for tests that want the raw violation predicate.
export { isViolated };
