import { domainSize, indexToValue, type Domain, type FieldDef } from "./model.ts";
import type { Box } from "./solver.ts";
import { fullSet, sizeOf, subtract, type ValSet } from "./valset.ts";

export function renderValSet(domain: Domain, s: ValSet): string {
  const size = domainSize(domain);
  if (sizeOf(s) === size) return "*";
  if (s.length === 0) return "(empty)";
  if (domain.kind === "enum") {
    const values: string[] = [];
    for (const [lo, hi] of s) {
      for (let i = lo; i <= hi; i++) values.push(domain.values[i]);
    }
    const complement = subtract(fullSet(size), s);
    const compSize = sizeOf(complement);
    if (compSize < values.length) {
      const missing: string[] = [];
      for (const [lo, hi] of complement) {
        for (let i = lo; i <= hi; i++) missing.push(domain.values[i]);
      }
      return `not {${missing.join(", ")}}`;
    }
    return values.length === 1 ? values[0] : `{${values.join(", ")}}`;
  }
  return s
    .map(([lo, hi]) => {
      const a = indexToValue(domain, lo);
      const b = indexToValue(domain, hi);
      return lo === hi ? `${a}` : `${a}..${b}`;
    })
    .join(" | ");
}

/** Render a box as `field: set` pairs, skipping unconstrained fields. */
export function renderBox(fields: FieldDef[], box: Box): string {
  const parts: string[] = [];
  fields.forEach((fd, f) => {
    if (sizeOf(box[f]) === domainSize(fd.domain)) return;
    parts.push(`${fd.name}: ${renderValSet(fd.domain, box[f])}`);
  });
  return parts.length === 0 ? "(any input)" : parts.join(", ");
}

export function renderAssignment(assignment: Record<string, string | number>): string {
  return Object.entries(assignment)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : v}`)
    .join(", ");
}

export function formatCount(n: number): string {
  if (n >= Number.MAX_SAFE_INTEGER) return `>= ${Number.MAX_SAFE_INTEGER.toExponential(2)}`;
  return n.toLocaleString("en-US");
}
