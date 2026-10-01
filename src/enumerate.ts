import { Rational } from "./rational.ts";
import { satisfies } from "./system.ts";
import type { CompiledSystem, Ineq } from "./system.ts";

/**
 * Small-domain enumeration verifier.
 *
 * For systems with few variables (default <= 4) and all error bounds known,
 * independently re-checks the joint verdict by exact enumeration:
 *
 * - Universal check: the max of a linear form over a box is attained at a
 *   box vertex, so enumerating all 2^n vertices is exact and complete.
 * - Existential check: a bounded non-empty polyhedron has a vertex, so
 *   enumerating all intersections of n boundary hyperplanes (constraints +
 *   box bounds) decides feasibility of the CLOSURE. Strict inequalities can
 *   make the open region feasible while every closure vertex is infeasible,
 *   so a midpoint grid is enumerated as well; if nothing is found while
 *   strict constraints are present, the report says "inconclusive" rather
 *   than guessing.
 */

export interface EnumerationReport {
  readonly checked: boolean;
  readonly skippedReason?: string;
  readonly verticesChecked: number;
  /** Per-constraint universal verdict from box-vertex enumeration. */
  readonly universalHolds: readonly boolean[];
  readonly existential: "feasible" | "infeasible" | "inconclusive";
  readonly feasiblePoint?: readonly Rational[];
  readonly pointsTested: number;
}

/** Solve an exact n x n rational linear system; null if singular. */
export function solveSquare(
  rows: readonly (readonly Rational[])[],
  rhs: readonly Rational[],
): Rational[] | null {
  const n = rows.length;
  const a: Rational[][] = rows.map((r, i) => [...r, rhs[i]]);
  for (let col = 0; col < n; col++) {
    let pivot = -1;
    for (let r = col; r < n; r++) {
      if (!a[r][col].isZero()) {
        pivot = r;
        break;
      }
    }
    if (pivot === -1) return null;
    [a[col], a[pivot]] = [a[pivot], a[col]];
    const inv = Rational.one().div(a[col][col]);
    a[col] = a[col].map((v) => v.mul(inv));
    for (let r = 0; r < n; r++) {
      if (r === col || a[r][col].isZero()) continue;
      const f = a[r][col];
      a[r] = a[r].map((v, i) => v.sub(f.mul(a[col][i])));
    }
  }
  return a.map((row) => row[n]);
}

function* combinations(m: number, k: number): Generator<number[]> {
  const idx = Array.from({ length: k }, (_, i) => i);
  if (k > m) return;
  while (true) {
    yield [...idx];
    let i = k - 1;
    while (i >= 0 && idx[i] === m - k + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let j = i + 1; j < k; j++) idx[j] = idx[j - 1] + 1;
  }
}

export interface EnumerateOptions {
  readonly maxVariables?: number;
  readonly maxBoundaryFaces?: number;
}

export function verifyByEnumeration(
  system: CompiledSystem,
  options: EnumerateOptions = {},
): EnumerationReport {
  const maxVars = options.maxVariables ?? 4;
  const maxFaces = options.maxBoundaryFaces ?? 24;
  const n = system.variables.length;
  const base: EnumerationReport = {
    checked: false,
    verticesChecked: 0,
    universalHolds: [],
    existential: "inconclusive",
    pointsTested: 0,
  };

  if (n > maxVars) {
    return { ...base, skippedReason: `too many variables (${n} > ${maxVars})` };
  }
  if (system.variables.some((v) => v.lo === null || v.hi === null)) {
    return { ...base, skippedReason: "unbounded error (unknown error bound)" };
  }

  const lo = system.variables.map((v) => v.lo as Rational);
  const hi = system.variables.map((v) => v.hi as Rational);

  // 1) Universal check: enumerate all 2^n box vertices.
  let verticesChecked = 0;
  const universalHolds = system.constraints.map(() => true);
  const totalVertices = 2 ** n;
  for (let mask = 0; mask < totalVertices; mask++) {
    const vertex = lo.map((l, i) => ((mask >> i) & 1) === 0 ? l : hi[i]);
    verticesChecked++;
    system.constraints.forEach((cc, ci) => {
      if (!satisfies(cc.ineq, vertex)) universalHolds[ci] = false;
    });
  }

  // 2) Existential check: boundary faces = user constraints (as equalities)
  //    plus box bounds.
  interface Face {
    coeffs: readonly Rational[];
    rhs: Rational;
  }
  const faces: Face[] = [];
  for (const cc of system.constraints) {
    faces.push({ coeffs: cc.ineq.coeffs, rhs: cc.ineq.rhs });
  }
  for (let i = 0; i < n; i++) {
    const unit = lo.map((_, j) => (j === i ? Rational.one() : Rational.zero()));
    faces.push({ coeffs: unit, rhs: lo[i] });
    faces.push({ coeffs: unit, rhs: hi[i] });
  }
  if (faces.length > maxFaces) {
    return {
      ...base,
      verticesChecked,
      universalHolds,
      skippedReason: `too many boundary faces (${faces.length} > ${maxFaces})`,
    };
  }

  const allIneqs: readonly Ineq[] = system.inequalities;
  let pointsTested = 0;
  let feasiblePoint: readonly Rational[] | undefined;
  const tryPoint = (p: readonly Rational[]): boolean => {
    pointsTested++;
    if (allIneqs.every((ineq) => satisfies(ineq, p))) {
      feasiblePoint = p;
      return true;
    }
    return false;
  };

  // Candidate 1: polyhedron vertices (intersections of n faces).
  if (n > 0) {
    for (const combo of combinations(faces.length, n)) {
      const rows = combo.map((f) => faces[f].coeffs);
      const rhs = combo.map((f) => faces[f].rhs);
      const sol = solveSquare(rows, rhs);
      if (sol !== null && tryPoint(sol)) break;
    }
  } else {
    tryPoint([]);
  }

  // Candidate 2: midpoint grid (catches strict-only interior feasibility).
  if (feasiblePoint === undefined) {
    const mid = lo.map((l, i) => l.add(hi[i]).div(Rational.of(2n)));
    const choices = lo.map((l, i) => [l, mid[i], hi[i]]);
    const total = 3 ** n;
    outer: for (let t = 0; t < total; t++) {
      let rem = t;
      const p: Rational[] = [];
      for (let i = 0; i < n; i++) {
        p.push(choices[i][rem % 3]);
        rem = Math.floor(rem / 3);
      }
      if (tryPoint(p)) break outer;
    }
  }

  const hasStrict = system.constraints.some((cc) => cc.ineq.strict);
  const existential =
    feasiblePoint !== undefined
      ? "feasible"
      : hasStrict
        ? "inconclusive"
        : "infeasible";

  return {
    checked: true,
    verticesChecked,
    universalHolds,
    existential,
    feasiblePoint,
    pointsTested,
  };
}

/**
 * Cross-check an FM/joint verdict against enumeration. Returns a list of
 * inconsistencies (empty = consistent). Enumeration only runs on small,
 * fully bounded domains; a skipped enumeration is not an inconsistency.
 */
export function crossCheck(
  system: CompiledSystem,
  joint: { status: string },
  report: EnumerationReport,
): string[] {
  const problems: string[] = [];
  if (!report.checked) return problems;

  if (joint.status === "determinate-pass" && report.universalHolds.some((h) => !h)) {
    problems.push("joint says determinate-pass but enumeration found a violating vertex");
  }
  if (joint.status === "determinate-fail" && report.existential === "feasible") {
    problems.push("joint says determinate-fail but enumeration found a feasible point");
  }
  if (
    (joint.status === "determinate-pass" || joint.status === "mixed") &&
    report.existential === "infeasible"
  ) {
    problems.push("joint found a satisfying assignment but enumeration found none");
  }
  return problems;
}
