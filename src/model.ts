/**
 * Problem model and worst-case error bound.
 *
 * Error model:
 *  - Every error source is a shared (correlated) scalar. A source used by
 *    exactly one component is effectively independent; a source used by many
 *    components (e.g. shared calibration) couples them: their contributions
 *    are summed INSIDE one interval before combining with other sources.
 *  - Per source s:  I_s = sum over components c of  sens[c][s] * err_c .
 *    Scaling an interval by a negative sensitivity flips and scales it by
 *    |sens| — so negative sensitivities contribute by absolute value, while
 *    cancellation WITHIN one shared source is still accounted exactly.
 *  - Total: T = sum over sources of I_s (interval addition; exact because
 *    distinct sources are independent). Worst-case error W = max(|T.lo|, |T.hi|).
 *  - This is the exact range of the total error given the interval inputs —
 *    a tight bound. We never add per-component absolute values across a
 *    shared source and call that tight.
 *
 * Integer grade selection and interval error directions are separate axes:
 * grades are discrete choices, intervals carry direction (lo/hi). The solver
 * never conflates them.
 */

import * as R from "./rational.ts";

export interface Interval {
  readonly lo: R.Rational;
  readonly hi: R.Rational;
}

export const ZERO_INTERVAL: Interval = { lo: R.ZERO, hi: R.ZERO };

export function interval(lo: R.Rational, hi: R.Rational): Interval {
  if (R.cmp(lo, hi) > 0) throw new Error("interval: lo > hi");
  return { lo, hi };
}

export function intervalAdd(a: Interval, b: Interval): Interval {
  return { lo: R.add(a.lo, b.lo), hi: R.add(a.hi, b.hi) };
}

/** Scale by a signed rational; negative flips endpoints (abs contribution). */
export function intervalScale(i: Interval, k: R.Rational): Interval {
  const a = R.mul(i.lo, k);
  const b = R.mul(i.hi, k);
  return R.cmp(a, b) <= 0 ? { lo: a, hi: b } : { lo: b, hi: a };
}

export function intervalHull(a: Interval, b: Interval): Interval {
  return { lo: R.min(a.lo, b.lo), hi: R.max(a.hi, b.hi) };
}

/** Worst-case absolute magnitude reachable inside the interval. */
export function intervalWorstAbs(i: Interval): R.Rational {
  return R.max(R.abs(i.lo), R.abs(i.hi));
}

export interface GradeRef {
  component: string;
  grade: string;
}

export interface GradeSpec {
  id: string;
  cost: string;
  /** per-source error interval [lo, hi]; missing source = [0,0] */
  errors?: Record<string, [string, string]>;
}

export interface ComponentSpec {
  id: string;
  /** per-source rational sensitivity; missing source = 0 */
  sensitivities?: Record<string, string>;
  grades: GradeSpec[];
}

export interface ProblemSpec {
  errorLimit: string;
  components: ComponentSpec[];
  /** pairs of grade choices that cannot coexist */
  exclusions?: { a: GradeRef; b: GradeRef }[];
  /** choosing `if` forces `requires` */
  dependencies?: { if: GradeRef; requires: GradeRef }[];
}

export interface Grade {
  id: string;
  cost: R.Rational;
  errors: ReadonlyMap<string, Interval>;
}

export interface Component {
  id: string;
  sensitivities: ReadonlyMap<string, R.Rational>;
  grades: Grade[];
}

export interface Problem {
  errorLimit: R.Rational;
  sources: string[];
  components: Component[];
  componentIndex: ReadonlyMap<string, number>;
  exclusions: { a: GradeRef; b: GradeRef }[];
  dependencies: { if: GradeRef; requires: GradeRef }[];
  /** canonical JSON used for hashing / certificate staleness checks */
  canonical: string;
}

function gradeKey(r: GradeRef): string {
  return `${r.component}${r.grade}`;
}

export function parseProblem(spec: ProblemSpec): Problem {
  if (!spec || typeof spec !== "object") throw new Error("problem: not an object");
  const errorLimit = R.parse(spec.errorLimit);
  if (R.cmp(errorLimit, R.ZERO) < 0) throw new Error("problem: negative errorLimit");
  if (!Array.isArray(spec.components) || spec.components.length === 0) {
    throw new Error("problem: no components");
  }

  const sources = new Set<string>();
  const components: Component[] = [];
  const componentIndex = new Map<string, number>();

  for (const cs of spec.components) {
    if (componentIndex.has(cs.id)) throw new Error(`problem: duplicate component ${cs.id}`);
    if (!Array.isArray(cs.grades) || cs.grades.length === 0) {
      throw new Error(`problem: component ${cs.id} has no grades`);
    }
    const sensitivities = new Map<string, R.Rational>();
    for (const [s, v] of Object.entries(cs.sensitivities ?? {})) {
      sensitivities.set(s, R.parse(v));
      sources.add(s);
    }
    const gradeIds = new Set<string>();
    const grades: Grade[] = cs.grades.map((gs) => {
      if (gradeIds.has(gs.id)) throw new Error(`problem: duplicate grade ${cs.id}/${gs.id}`);
      gradeIds.add(gs.id);
      const errors = new Map<string, Interval>();
      for (const [s, range] of Object.entries(gs.errors ?? {})) {
        if (!Array.isArray(range) || range.length !== 2) {
          throw new Error(`problem: bad error range ${cs.id}/${gs.id}/${s}`);
        }
        errors.set(s, interval(R.parse(range[0]), R.parse(range[1])));
        sources.add(s);
      }
      return { id: gs.id, cost: R.parse(gs.cost), errors };
    });
    componentIndex.set(cs.id, components.length);
    components.push({ id: cs.id, sensitivities, grades });
  }

  const checkRef = (r: GradeRef, what: string) => {
    const ci = componentIndex.get(r.component);
    if (ci === undefined) throw new Error(`problem: ${what}: unknown component ${r.component}`);
    if (!components[ci].grades.some((g) => g.id === r.grade)) {
      throw new Error(`problem: ${what}: unknown grade ${r.component}/${r.grade}`);
    }
  };
  const exclusions = spec.exclusions ?? [];
  const dependencies = spec.dependencies ?? [];
  exclusions.forEach((e, i) => {
    checkRef(e.a, `exclusions[${i}]`);
    checkRef(e.b, `exclusions[${i}]`);
    if (gradeKey(e.a) === gradeKey(e.b)) throw new Error(`problem: exclusions[${i}] self-exclusion`);
  });
  dependencies.forEach((d, i) => {
    checkRef(d.if, `dependencies[${i}]`);
    checkRef(d.requires, `dependencies[${i}]`);
  });

  // Canonical form: stable key order so the hash is objective-sensitive.
  const canonical = canonicalJson({
    errorLimit: R.toCanonical(errorLimit),
    components: components.map((c) => ({
      id: c.id,
      sensitivities: Object.fromEntries(
        [...c.sensitivities].map(([k, v]) => [k, R.toCanonical(v)]).sort(([a], [b]) => (a < b ? -1 : 1)),
      ),
      grades: c.grades.map((g) => ({
        id: g.id,
        cost: R.toCanonical(g.cost),
        errors: Object.fromEntries(
          [...g.errors]
            .map(([k, v]) => [k, [R.toCanonical(v.lo), R.toCanonical(v.hi)]])
            .sort(([a], [b]) => (a < b ? -1 : 1)),
        ),
      })),
    })),
    exclusions,
    dependencies,
  });

  return {
    errorLimit,
    sources: [...sources].sort(),
    components,
    componentIndex,
    exclusions,
    dependencies,
    canonical,
  };
}

function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : 1));
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonicalJson(v)}`).join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}

/** FNV-1a 64-bit over the canonical string, hex. Dependency-free integrity tag. */
export function problemHash(p: Problem): string {
  let h = 0xcbf29ce484222325n;
  const prime = 0x100000001b3n;
  const mask = 0xffffffffffffffffn;
  for (const ch of p.canonical) {
    h ^= BigInt(ch.codePointAt(0)!);
    h = (h * prime) & mask;
  }
  return h.toString(16).padStart(16, "0");
}

export type Assignment = ReadonlyMap<number, number>; // component index -> grade index

export interface ContributionBreakdown {
  /** per component: per source scaled interval */
  perComponent: { component: string; grade: string; perSource: { source: string; interval: Interval }[] }[];
  perSource: { source: string; interval: Interval }[];
  total: Interval;
  worst: R.Rational;
  feasible: boolean;
}

/** Exact worst-case analysis of a complete assignment. */
export function analyze(problem: Problem, assignment: Assignment): ContributionBreakdown {
  const perSourceIntervals = new Map<string, Interval>();
  for (const s of problem.sources) perSourceIntervals.set(s, ZERO_INTERVAL);

  const perComponent = problem.components.map((comp, ci) => {
    const gi = assignment.get(ci);
    if (gi === undefined) throw new Error(`analyze: component ${comp.id} unassigned`);
    const grade = comp.grades[gi];
    const perSource = problem.sources.map((s) => {
      const err = grade.errors.get(s) ?? ZERO_INTERVAL;
      const sens = comp.sensitivities.get(s) ?? R.ZERO;
      const scaled = intervalScale(err, sens);
      perSourceIntervals.set(s, intervalAdd(perSourceIntervals.get(s)!, scaled));
      return { source: s, interval: scaled };
    });
    return { component: comp.id, grade: grade.id, perSource };
  });

  let total = ZERO_INTERVAL;
  const perSource = problem.sources.map((s) => {
    const i = perSourceIntervals.get(s)!;
    total = intervalAdd(total, i);
    return { source: s, interval: i };
  });
  const worst = intervalWorstAbs(total);
  return { perComponent, perSource, total, worst, feasible: R.cmp(worst, problem.errorLimit) <= 0 };
}

export function totalCost(problem: Problem, assignment: Assignment): R.Rational {
  let sum = R.ZERO;
  for (const [ci, gi] of assignment) sum = R.add(sum, problem.components[ci].grades[gi].cost);
  return sum;
}

/** Grade-ref lookup used by constraint checks. */
export function refIndex(problem: Problem, r: GradeRef): { ci: number; gi: number } {
  const ci = problem.componentIndex.get(r.component)!;
  const gi = problem.components[ci].grades.findIndex((g) => g.id === r.grade);
  return { ci, gi };
}
