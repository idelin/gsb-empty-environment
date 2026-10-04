/**
 * Brute-force vertex enumeration of { x >= 0 : A x <= b } over exact
 * rationals. Used only to cross-check the simplex solver on small instances.
 */
import { Frac, ZERO, ONE, dot } from "./fraction";
import { LP } from "./simplex";

export interface Enumeration {
  vertices: Frac[][];
  /** max c·x over vertices; null when the feasible region is empty */
  maxValue: Frac | null;
}

function* combinations(total: number, choose: number): Generator<number[]> {
  const idx: number[] = [];
  for (let i = 0; i < choose; i++) idx.push(i);
  for (;;) {
    yield idx.slice();
    let i = choose - 1;
    while (i >= 0 && idx[i] === total - choose + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let k = i + 1; k < choose; k++) idx[k] = idx[k - 1] + 1;
  }
}

/** Solve an n x n system exactly; returns null when singular. */
export function solveSquare(M: Frac[][], r: Frac[]): Frac[] | null {
  const n = r.length;
  const a = M.map((row, i) => row.concat([r[i]]));
  for (let col = 0; col < n; col++) {
    let p = -1;
    for (let i = col; i < n; i++) {
      if (!a[i][col].isZero()) {
        p = i;
        break;
      }
    }
    if (p < 0) return null;
    [a[col], a[p]] = [a[p], a[col]];
    const inv = ONE.div(a[col][col]);
    for (let j = col; j <= n; j++) a[col][j] = a[col][j].mul(inv);
    for (let i = 0; i < n; i++) {
      if (i === col || a[i][col].isZero()) continue;
      const f = a[i][col];
      for (let j = col; j <= n; j++) a[i][j] = a[i][j].sub(f.mul(a[col][j]));
    }
  }
  return a.map((row) => row[n]);
}

export function enumerateVertices(lp: LP): Enumeration {
  const m = lp.A.length;
  const n = lp.c.length;

  // Constraints: A x <= b together with -x_j <= 0.
  const cons: { a: Frac[]; rhs: Frac }[] = [];
  for (let i = 0; i < m; i++) cons.push({ a: lp.A[i], rhs: lp.b[i] });
  for (let j = 0; j < n; j++) {
    const a = new Array<Frac>(n).fill(ZERO);
    a[j] = ONE.neg();
    cons.push({ a, rhs: ZERO });
  }

  if (n === 0) {
    const feasible = cons.every((con) => ZERO.cmp(con.rhs) <= 0);
    return {
      vertices: feasible ? [[]] : [],
      maxValue: feasible ? ZERO : null,
    };
  }

  const vertices: Frac[][] = [];
  const seen = new Set<string>();
  for (const subset of combinations(cons.length, n)) {
    const M = subset.map((k) => cons[k].a);
    const r = subset.map((k) => cons[k].rhs);
    const x = solveSquare(M, r);
    if (x === null) continue;
    let feasible = true;
    for (const con of cons) {
      if (dot(con.a, x).cmp(con.rhs) > 0) {
        feasible = false;
        break;
      }
    }
    if (!feasible) continue;
    const key = x.map((f) => f.toString()).join(",");
    if (!seen.has(key)) {
      seen.add(key);
      vertices.push(x);
    }
  }

  let maxValue: Frac | null = null;
  for (const v of vertices) {
    const val = dot(lp.c, v);
    if (maxValue === null || val.cmp(maxValue) > 0) maxValue = val;
  }
  return { vertices, maxValue };
}
