/**
 * Independent certificate checker. Verifies only algebraic conditions with
 * exact rational arithmetic; it never runs or re-derives the simplex method.
 */
import { Frac, ZERO, dot } from "./fraction";
import { LP, SolveResult } from "./simplex";

export interface CheckOutcome {
  ok: boolean;
  errors: string[];
}

function matVecMul(A: Frac[][], x: Frac[]): Frac[] {
  return A.map((row) => dot(row, x));
}

function matTVecMul(A: Frac[][], y: Frac[], n: number): Frac[] {
  const out: Frac[] = [];
  for (let j = 0; j < n; j++) {
    let s = ZERO;
    for (let i = 0; i < A.length; i++) s = s.add(A[i][j].mul(y[i]));
    out.push(s);
  }
  return out;
}

export function checkResult(lp: LP, res: SolveResult): CheckOutcome {
  const errors: string[] = [];
  const m = lp.A.length;
  const n = lp.c.length;
  const err = (msg: string) => errors.push(msg);

  const needVec = (v: Frac[] | undefined, len: number, name: string): Frac[] | null => {
    if (!v) {
      err(`missing ${name}`);
      return null;
    }
    if (v.length !== len) {
      err(`${name} has length ${v.length}, expected ${len}`);
      return null;
    }
    return v;
  };

  const checkPrimalFeasible = (x: Frac[], label: string) => {
    for (let j = 0; j < n; j++)
      if (x[j].sign() < 0) err(`${label}: x[${j}] = ${x[j]} < 0`);
    const Ax = matVecMul(lp.A, x);
    for (let i = 0; i < m; i++)
      if (Ax[i].cmp(lp.b[i]) > 0)
        err(`${label}: row ${i}: (Ax)_${i} = ${Ax[i]} > b_${i} = ${lp.b[i]}`);
  };

  switch (res.status) {
    case "optimal": {
      const x = needVec(res.x, n, "x");
      const y = needVec(res.y, m, "y");
      if (res.value === undefined) err("missing value");
      if (!x || !y || res.value === undefined) break;

      checkPrimalFeasible(x, "primal");

      for (let i = 0; i < m; i++)
        if (y[i].sign() < 0) err(`dual: y[${i}] = ${y[i]} < 0`);
      const Aty = matTVecMul(lp.A, y, n);
      for (let j = 0; j < n; j++)
        if (Aty[j].cmp(lp.c[j]) < 0)
          err(`dual: (Aᵀy)_${j} = ${Aty[j]} < c_${j} = ${lp.c[j]}`);

      const cx = dot(lp.c, x);
      const by = dot(lp.b, y);
      if (by.cmp(cx) !== 0)
        err(`strong duality violated: b·y = ${by} != c·x = ${cx}`);
      if (res.value.cmp(cx) !== 0)
        err(`reported value ${res.value} != c·x = ${cx}`);
      break;
    }

    case "infeasible": {
      const y = needVec(res.y, m, "y");
      if (!y) break;
      for (let i = 0; i < m; i++)
        if (y[i].sign() < 0) err(`farkas: y[${i}] = ${y[i]} < 0`);
      const Aty = matTVecMul(lp.A, y, n);
      for (let j = 0; j < n; j++)
        if (Aty[j].sign() < 0) err(`farkas: (Aᵀy)_${j} = ${Aty[j]} < 0`);
      const by = dot(lp.b, y);
      if (by.sign() >= 0) err(`farkas: b·y = ${by} must be < 0`);
      break;
    }

    case "unbounded": {
      const x = needVec(res.x, n, "x");
      const d = needVec(res.d, n, "d");
      if (!x || !d) break;
      checkPrimalFeasible(x, "unbounded point");
      for (let j = 0; j < n; j++)
        if (d[j].sign() < 0) err(`ray: d[${j}] = ${d[j]} < 0`);
      const Ad = matVecMul(lp.A, d);
      for (let i = 0; i < m; i++)
        if (Ad[i].sign() > 0) err(`ray: (Ad)_${i} = ${Ad[i]} > 0`);
      const cd = dot(lp.c, d);
      if (cd.sign() <= 0) err(`ray: c·d = ${cd} must be > 0`);
      break;
    }

    case "budget_exhausted":
      // No certificate is claimed; nothing to verify.
      break;
  }

  return { ok: errors.length === 0, errors };
}
