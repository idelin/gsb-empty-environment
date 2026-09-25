// Idempotency counterexample search over finite input domains.
//
// For each rule we derive a small finite domain of candidate values per
// concrete target path (instantiated against a sample "shape" tree), then
// enumerate the cartesian product under a budget. Three outcomes:
//   "proven"  - the whole product was enumerated, no counterexample.
//   "refuted" - a concrete input with normalize^2(x) != normalize(x).
//   "unknown" - the budget ran out first. NEVER reported as proven.

import { deepEqual, getAt, pathToString, setAt, walk } from "./json.ts";
import type { JsonValue, PathSegment } from "./json.ts";
import { applyRules } from "./normalize.ts";
import { matchPattern } from "./rules.ts";
import type { DateRule, Rule } from "./rules.ts";

export type DomainMap = Map<string, JsonValue[]>;

function dedupe(values: JsonValue[]): JsonValue[] {
  const out: JsonValue[] = [];
  for (const v of values) {
    if (!out.some((existing) => deepEqual(existing, v))) out.push(v);
  }
  return out;
}

function validSampleForFormat(format: string): string {
  return format
    .replace(/YYYY/g, "2024")
    .replace(/MM/g, "03")
    .replace(/DD/g, "04");
}

function domainForRule(rule: Rule): JsonValue[] {
  switch (rule.kind) {
    case "whitespace":
      // Includes null and non-strings: null must never be equated with
      // whitespace, and non-strings must pass through untouched.
      return ["  padded  ", "a  b\tc", "clean", "", "   ", null, 7];
    case "date": {
      const formats = (rule as DateRule).formats;
      const valids = formats.map(validSampleForFormat);
      const iso = "2024-03-04";
      return dedupe([
        ...valids,
        iso,
        "2024-13-40", // matches shape, invalid calendar date
        "not-a-date", // matches nothing
        "",
        null,
        20240304,
      ]);
    }
    case "enum-alias": {
      const entries = Object.entries(rule.aliases);
      const keys = entries.map(([k]) => k);
      const values = entries.map(([, v]) => v);
      return dedupe([...keys, ...values, "unlisted", "", null, 0]);
    }
    case "copy":
      return ["sample", "", null, 0];
  }
}

/** Concrete target paths of a rule, instantiated against a shape tree. */
export function concretePaths(rule: Rule, shape: JsonValue): PathSegment[][] {
  const patterns =
    rule.kind === "copy" ? [rule.from, rule.to] : rule.targets;
  const seen = new Map<string, PathSegment[]>();
  for (const { path } of walk(shape)) {
    for (const pattern of patterns) {
      if (matchPattern(pattern, path)) {
        seen.set(pathToString(path), path);
        break;
      }
    }
  }
  return [...seen.values()];
}

/** Derives a finite candidate-value domain per concrete path. */
export function deriveDomains(rules: readonly Rule[], shape: JsonValue): DomainMap {
  const domains: DomainMap = new Map();
  for (const rule of rules) {
    for (const path of concretePaths(rule, shape)) {
      const key = pathToString(path);
      const existing = domains.get(key) ?? [];
      domains.set(key, dedupe([...existing, ...domainForRule(rule)]));
    }
  }
  return domains;
}

export interface Counterexample {
  input: JsonValue;
  once: JsonValue;
  twice: JsonValue;
  /** First path where the two runs disagree. */
  path: string;
}

export interface IdempotencyReport {
  status: "proven" | "refuted" | "unknown";
  checked: number;
  /** Total combinations in the finite domain (may exceed budget). */
  total: number;
  counterexample?: Counterexample;
}

function firstDifference(a: JsonValue, b: JsonValue): string {
  const differing: PathSegment[][] = [];
  for (const { path, value } of walk(a)) {
    const other = getAt(b, path);
    if (!other.found || !deepEqual(value, other.value as JsonValue)) {
      differing.push(path);
    }
  }
  for (const { path } of walk(b)) {
    if (!getAt(a, path).found) differing.push(path);
  }
  // The most specific (deepest) differing path identifies the field.
  differing.sort((x, y) => y.length - x.length);
  return differing.length > 0 ? pathToString(differing[0]) || "/" : "/";
}

export function searchIdempotency(
  rules: readonly Rule[],
  shape: JsonValue,
  budget = 100_000,
  domains: DomainMap = deriveDomains(rules, shape),
): IdempotencyReport {
  const keys = [...domains.keys()].sort();
  const lists = keys.map((k) => domains.get(k)!);
  const total = lists.reduce((acc, list) => acc * Math.max(list.length, 1), 1);

  const indices = new Array<number>(keys.length).fill(0);
  let checked = 0;

  for (;;) {
    // Build the input tree for the current odometer position.
    let input = shape;
    keys.forEach((key, i) => {
      const path = key === "" ? [] : (key.slice(1).split("/").map((seg) => {
        const n = Number(seg);
        return Number.isInteger(n) && String(n) === seg ? n : seg;
      }) as PathSegment[]);
      input = setAt(input, path, lists[i][indices[i]]);
    });

    const once = applyRules(input, rules);
    const twice = applyRules(once.output, rules);
    checked++;

    if (!deepEqual(once.output, twice.output)) {
      return {
        status: "refuted",
        checked,
        total,
        counterexample: {
          input,
          once: once.output,
          twice: twice.output,
          path: firstDifference(once.output, twice.output),
        },
      };
    }
    // Advance the odometer.
    let pos = indices.length - 1;
    while (pos >= 0) {
      indices[pos]++;
      if (indices[pos] < lists[pos].length) break;
      indices[pos] = 0;
      pos--;
    }
    if (pos < 0) {
      return { status: "proven", checked, total };
    }
    if (checked >= budget) {
      return { status: "unknown", checked, total };
    }
  }
}
