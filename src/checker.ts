/**
 * 独立证书检查器：只做代数验证（精确有理数运算），不调用任何求解器。
 *
 * - optimal:    x >= 0, Ax <= b, y >= 0, Aᵀy >= c, b·y = c·x
 * - infeasible: y >= 0, Aᵀy >= 0, b·y < 0        （Farkas 引理）
 * - unbounded:  x 可行, d >= 0, Ad <= 0, c·d > 0
 */

import type { Rat } from "./rational.ts";
import { ZERO, add, mul, cmp, isNegative, isPositive, formatRat } from "./rational.ts";
import type { LP, SolveResult } from "./simplex.ts";

export interface CheckResult {
  ok: boolean;
  violations: string[];
}

function dot(u: readonly Rat[], v: readonly Rat[]): Rat {
  let acc = ZERO;
  for (let i = 0; i < u.length; i++) acc = add(acc, mul(u[i], v[i]));
  return acc;
}

/** 计算 A·x，长度 m。 */
function matVec(A: readonly Rat[][], x: readonly Rat[]): Rat[] {
  return A.map((row) => dot(row, x));
}

/** 计算 Aᵀ·y，长度 n。 */
function matTVec(A: readonly Rat[][], y: readonly Rat[], n: number): Rat[] {
  const out: Rat[] = new Array(n).fill(ZERO);
  for (let i = 0; i < A.length; i++) {
    for (let j = 0; j < n; j++) {
      out[j] = add(out[j], mul(A[i][j], y[i]));
    }
  }
  return out;
}

function checkNonNeg(v: readonly Rat[], name: string, violations: string[]): void {
  v.forEach((value, i) => {
    if (isNegative(value)) violations.push(`${name}[${i}] = ${formatRat(value)} < 0`);
  });
}

function checkPrimalFeasible(lp: LP, x: Rat[], violations: string[]): void {
  const n = lp.c.length;
  if (x.length !== n) {
    violations.push(`x has length ${x.length}, expected ${n}`);
    return;
  }
  checkNonNeg(x, "x", violations);
  const ax = matVec(lp.A, x);
  for (let i = 0; i < lp.b.length; i++) {
    if (cmp(ax[i], lp.b[i]) > 0) {
      violations.push(
        `constraint ${i}: (Ax)[${i}] = ${formatRat(ax[i])} > b[${i}] = ${formatRat(lp.b[i])}`,
      );
    }
  }
}

export function checkOptimal(lp: LP, x: Rat[], y: Rat[]): CheckResult {
  const violations: string[] = [];
  const n = lp.c.length;
  const m = lp.b.length;
  if (y.length !== m) {
    violations.push(`y has length ${y.length}, expected ${m}`);
    return { ok: false, violations };
  }
  checkPrimalFeasible(lp, x, violations);
  checkNonNeg(y, "y", violations);
  const aty = matTVec(lp.A, y, n);
  for (let j = 0; j < n; j++) {
    if (cmp(aty[j], lp.c[j]) < 0) {
      violations.push(
        `dual constraint ${j}: (Aᵀy)[${j}] = ${formatRat(aty[j])} < c[${j}] = ${formatRat(lp.c[j])}`,
      );
    }
  }
  const cx = dot(lp.c, x);
  const by = dot(lp.b, y);
  if (cmp(cx, by) !== 0) {
    violations.push(`strong duality: c·x = ${formatRat(cx)} != b·y = ${formatRat(by)}`);
  }
  return { ok: violations.length === 0, violations };
}

export function checkInfeasible(lp: LP, y: Rat[]): CheckResult {
  const violations: string[] = [];
  const n = lp.c.length;
  const m = lp.b.length;
  if (y.length !== m) {
    violations.push(`y has length ${y.length}, expected ${m}`);
    return { ok: false, violations };
  }
  checkNonNeg(y, "y", violations);
  const aty = matTVec(lp.A, y, n);
  for (let j = 0; j < n; j++) {
    if (isNegative(aty[j])) {
      violations.push(`(Aᵀy)[${j}] = ${formatRat(aty[j])} < 0`);
    }
  }
  const by = dot(lp.b, y);
  if (!isNegative(by)) {
    violations.push(`b·y = ${formatRat(by)} is not < 0`);
  }
  return { ok: violations.length === 0, violations };
}

export function checkUnbounded(lp: LP, x: Rat[], d: Rat[]): CheckResult {
  const violations: string[] = [];
  const n = lp.c.length;
  if (d.length !== n) {
    violations.push(`d has length ${d.length}, expected ${n}`);
    return { ok: false, violations };
  }
  checkPrimalFeasible(lp, x, violations);
  checkNonNeg(d, "d", violations);
  const ad = matVec(lp.A, d);
  for (let i = 0; i < lp.b.length; i++) {
    if (isPositive(ad[i])) {
      violations.push(`(Ad)[${i}] = ${formatRat(ad[i])} > 0`);
    }
  }
  const cd = dot(lp.c, d);
  if (!isPositive(cd)) {
    violations.push(`c·d = ${formatRat(cd)} is not > 0`);
  }
  return { ok: violations.length === 0, violations };
}

/** 按求解结果的状态分派到相应的代数检查。iteration_limit 无可验声明，返回 ok。 */
export function checkResult(lp: LP, result: SolveResult): CheckResult {
  switch (result.status) {
    case "optimal":
      return checkOptimal(lp, result.x ?? [], result.y ?? []);
    case "infeasible":
      return checkInfeasible(lp, result.y ?? []);
    case "unbounded":
      return checkUnbounded(lp, result.x ?? [], result.direction ?? []);
    case "iteration_limit":
      return { ok: true, violations: [] };
  }
}
