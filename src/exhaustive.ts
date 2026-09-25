/**
 * Independent exhaustive checker.
 *
 * This module deliberately shares no logic with the symbolic engine beyond
 * the point evaluator: it enumerates every assignment of a (small) domain
 * and computes satisfying sets directly. It is used to cross-check
 * containment verdicts, boolean combinations (intersection / union /
 * difference of satisfying sets) and the authenticity of counterexamples
 * produced by the interval-splitting engine.
 */

import type { Cond } from "./conditions.ts";
import {
  UNKNOWN,
  validateDomain,
  type Assignment,
  type DomainSpec,
  type Value,
} from "./domain.ts";
import { evalPoint } from "./eval.ts";

const DEFAULT_MAX_ASSIGNMENTS = 100_000;

/** All assignments of the domain, in lexicographic order (sorted attribute
 *  names; ints ascending; enums/sets in declaration order; UNKNOWN last). */
export function enumerateAssignments(
  domain: DomainSpec,
  maxAssignments: number = DEFAULT_MAX_ASSIGNMENTS,
): Assignment[] {
  validateDomain(domain);
  const names = Object.keys(domain).sort();
  const choices: (Value | typeof UNKNOWN)[][] = names.map((n) => {
    const d = domain[n];
    const base: (Value | typeof UNKNOWN)[] =
      d.kind === "int"
        ? Array.from(
            { length: Math.max(0, d.max - d.min + 1) },
            (_, i) => d.min + i,
          )
        : d.kind === "enum"
          ? [...d.values]
          : d.sets.map((s) => [...s]);
    return d.optional === true ? [...base, UNKNOWN] : base;
  });
  const total = choices.reduce((acc, c) => acc * Math.max(1, c.length), 1);
  if (total > maxAssignments) {
    throw new Error(
      `domain too large for exhaustive checking: ${total} assignments (limit ${maxAssignments})`,
    );
  }
  const out: Assignment[] = [];
  const walk = (i: number, acc: Assignment): void => {
    if (i === names.length) {
      out.push({ ...acc });
      return;
    }
    for (const v of choices[i]) {
      acc[names[i]] = v;
      walk(i + 1, acc);
    }
  };
  walk(0, {});
  return out;
}

/** Stable key for an assignment (UNKNOWN serializes as "<unknown>"). */
export function assignmentKey(a: Assignment): string {
  const keys = Object.keys(a).sort();
  const parts = keys.map((k) => {
    const v = a[k];
    if (v === UNKNOWN) return `${k}:<unknown>`;
    if (Array.isArray(v)) return `${k}:{${[...v].map(String).sort().join(",")}}`;
    return `${k}:${String(v)}`;
  });
  return `{${parts.join(",")}}`;
}

/** Keys of assignments where the condition is definitely true. */
export function satisfyingKeys(cond: Cond, domain: DomainSpec): Set<string> {
  const out = new Set<string>();
  for (const a of enumerateAssignments(domain)) {
    if (evalPoint(cond, a) === "true") out.add(assignmentKey(a));
  }
  return out;
}

export type ExhaustiveContains =
  | { status: "contained" }
  | { status: "not-contained"; counterexample: Assignment }
  | { status: "unknown" };

/**
 * Containment by brute force. A concrete counterexample (inner true, outer
 * false) decides non-containment; otherwise any assignment whose truth value
 * is unknown prevents a proof and yields "unknown".
 */
export function exhaustiveContains(
  outer: Cond,
  inner: Cond,
  domain: DomainSpec,
): ExhaustiveContains {
  let sawUnknown = false;
  for (const a of enumerateAssignments(domain)) {
    const innerVal = evalPoint(inner, a);
    const outerVal = evalPoint(outer, a);
    if (innerVal === "true" && outerVal === "false") {
      return { status: "not-contained", counterexample: a };
    }
    if (innerVal === "unknown" || (innerVal === "true" && outerVal === "unknown")) {
      sawUnknown = true;
    }
  }
  return sawUnknown ? { status: "unknown" } : { status: "contained" };
}

export interface CounterexampleCheck {
  ok: boolean;
  detail: string;
}

/** Verify that a reported counterexample is genuine: it must belong to the
 *  domain, satisfy the inner condition and falsify the outer condition. */
export function verifyCounterexample(
  outer: Cond,
  inner: Cond,
  domain: DomainSpec,
  cex: Assignment,
): CounterexampleCheck {
  for (const [name, value] of Object.entries(cex)) {
    const d = domain[name];
    if (d === undefined) {
      return { ok: false, detail: `attribute "${name}" not in domain` };
    }
    if (value === UNKNOWN) {
      return { ok: false, detail: `attribute "${name}" is unknown` };
    }
    if (d.kind === "int") {
      if (
        typeof value !== "number" ||
        !Number.isInteger(value) ||
        value < d.min ||
        value > d.max
      ) {
        return { ok: false, detail: `"${name}" out of range [${d.min}, ${d.max}]` };
      }
    } else if (d.kind === "enum") {
      if (!d.values.includes(value as never)) {
        return { ok: false, detail: `"${name}" not an allowed enum value` };
      }
    } else {
      const asSet = value as readonly (string | number | boolean)[];
      const match = d.sets.some(
        (s) =>
          s.length === asSet.length && s.every((x) => asSet.includes(x)),
      );
      if (!match) {
        return { ok: false, detail: `"${name}" not an allowed set` };
      }
    }
  }
  const innerVal = evalPoint(inner, cex);
  const outerVal = evalPoint(outer, cex);
  if (innerVal !== "true") {
    return { ok: false, detail: `inner condition evaluates to ${innerVal}` };
  }
  if (outerVal !== "false") {
    return { ok: false, detail: `outer condition evaluates to ${outerVal}` };
  }
  return { ok: true, detail: "valid counterexample" };
}

export function unionKeys(a: Set<string>, b: Set<string>): Set<string> {
  return new Set([...a, ...b]);
}

export function intersectKeys(a: Set<string>, b: Set<string>): Set<string> {
  return new Set([...a].filter((x) => b.has(x)));
}

export function differenceKeys(a: Set<string>, b: Set<string>): Set<string> {
  return new Set([...a].filter((x) => !b.has(x)));
}

export interface BooleanLawsReport {
  andIsIntersection: boolean;
  orIsUnion: boolean;
  differenceIsAndNot: boolean;
}

/** Cross-check boolean combinations against set operations on satisfying
 *  sets: sat(A and B) == sat(A) n sat(B), sat(A or B) == sat(A) u sat(B),
 *  sat(A and not B) == sat(A) \ sat(B). The first two hold under three-valued
 *  semantics; the difference law requires a two-valued (non-optional) domain. */
export function checkBooleanLaws(
  andNode: Cond,
  orNode: Cond,
  diffNode: Cond,
  a: Cond,
  b: Cond,
  domain: DomainSpec,
): BooleanLawsReport {
  const satA = satisfyingKeys(a, domain);
  const satB = satisfyingKeys(b, domain);
  return {
    andIsIntersection:
      eqSet(satisfyingKeys(andNode, domain), intersectKeys(satA, satB)),
    orIsUnion: eqSet(satisfyingKeys(orNode, domain), unionKeys(satA, satB)),
    differenceIsAndNot:
      eqSet(satisfyingKeys(diffNode, domain), differenceKeys(satA, satB)),
  };
}

function eqSet(a: Set<string>, b: Set<string>): boolean {
  return a.size === b.size && [...a].every((x) => b.has(x));
}
