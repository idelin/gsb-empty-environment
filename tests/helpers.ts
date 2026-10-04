/** 测试辅助：字符串构造 LP、精确顶点枚举、确定性随机 LP 生成。 */

import type { Rat } from "../src/rational.ts";
import {
  ZERO,
  parseRat,
  rat,
  add,
  sub,
  mul,
  div,
  cmp,
  isZero,
  isNegative,
} from "../src/rational.ts";
import type { LP } from "../src/simplex.ts";

export function R(s: string): Rat {
  return parseRat(s);
}

export function lp(c: string[], A: string[][], b: string[]): LP {
  return { c: c.map(R), A: A.map((row) => row.map(R)), b: b.map(R) };
}

/** 精确高斯消元解 n×n 线性系统；奇异返回 null。 */
export function solveSquare(M: Rat[][], rhs: Rat[]): Rat[] | null {
  const n = rhs.length;
  const a = M.map((row, i) => [...row, rhs[i]]);
  for (let col = 0; col < n; col++) {
    let piv = -1;
    for (let r = col; r < n; r++) {
      if (!isZero(a[r][col])) {
        piv = r;
        break;
      }
    }
    if (piv < 0) return null;
    [a[col], a[piv]] = [a[piv], a[col]];
    for (let r = 0; r < n; r++) {
      if (r === col || isZero(a[r][col])) continue;
      const factor = div(a[r][col], a[col][col]);
      for (let k = col; k <= n; k++) {
        a[r][k] = sub(a[r][k], mul(factor, a[col][k]));
      }
    }
  }
  const x: Rat[] = new Array(n);
  for (let i = 0; i < n; i++) x[i] = div(a[i][n], a[i][i]);
  return x;
}

/** 枚举 {Ax<=b, x>=0} 的所有顶点（n 个活跃约束的交点中可行的那些）。 */
export function enumerateVertices(problem: LP): Rat[][] {
  const n = problem.c.length;
  const m = problem.b.length;
  // 超平面：A 的 m 行 + 非负约束 -e_j <= 0
  const planes: { row: Rat[]; rhs: Rat }[] = [];
  for (let i = 0; i < m; i++) planes.push({ row: problem.A[i], rhs: problem.b[i] });
  for (let j = 0; j < n; j++) {
    const row: Rat[] = new Array(n).fill(ZERO);
    row[j] = rat(-1n);
    planes.push({ row, rhs: ZERO });
  }
  const vertices: Rat[][] = [];
  const choose = (start: number, picked: number[]): void => {
    if (picked.length === n) {
      const M = picked.map((k) => planes[k].row);
      const rhs = picked.map((k) => planes[k].rhs);
      const x = n === 0 ? [] : solveSquare(M, rhs);
      if (x !== null && isFeasiblePoint(problem, x)) vertices.push(x);
      return;
    }
    for (let k = start; k <= planes.length - (n - picked.length); k++) {
      choose(k + 1, [...picked, k]);
    }
  };
  choose(0, []);
  return vertices;
}

export function isFeasiblePoint(problem: LP, x: Rat[]): boolean {
  for (let j = 0; j < x.length; j++) if (isNegative(x[j])) return false;
  for (let i = 0; i < problem.b.length; i++) {
    let lhs = ZERO;
    for (let j = 0; j < x.length; j++) lhs = add(lhs, mul(problem.A[i][j], x[j]));
    if (cmp(lhs, problem.b[i]) > 0) return false;
  }
  return true;
}

export function objectiveAt(problem: LP, x: Rat[]): Rat {
  let acc = ZERO;
  for (let j = 0; j < x.length; j++) acc = add(acc, mul(problem.c[j], x[j]));
  return acc;
}

/** 顶点上的最大目标值；无可行顶点返回 null。 */
export function maxOverVertices(problem: LP): Rat | null {
  const vertices = enumerateVertices(problem);
  if (vertices.length === 0) return null;
  let best = objectiveAt(problem, vertices[0]);
  for (const v of vertices.slice(1)) {
    const val = objectiveAt(problem, v);
    if (cmp(val, best) > 0) best = val;
  }
  return best;
}

/** 确定性 LCG 随机数。 */
export function makeRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1103515245 + 12345) >>> 0;
    return s;
  };
}

/** 生成小规模随机 LP，系数为 [-range, range] 内整数。 */
export function randomLp(rng: () => number, n: number, m: number, range = 5): LP {
  const ri = () => (rng() % (2 * range + 1)) - range;
  const c = Array.from({ length: n }, () => rat(BigInt(ri())));
  const A = Array.from({ length: m }, () => Array.from({ length: n }, () => rat(BigInt(ri()))));
  const b = Array.from({ length: m }, () => rat(BigInt(ri())));
  return { c, A, b };
}
