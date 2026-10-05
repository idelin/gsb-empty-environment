/**
 * 两阶段单纯形法（Bland 风格防循环），求解: maximize c·x, s.t. A x <= b, x >= 0。
 * b 允许为负（通过人工变量进入第一阶段）。
 */

const EPS = 1e-9;

export type LpStatus = 'optimal' | 'infeasible' | 'unbounded';

export interface LpResult {
  status: LpStatus;
  objective: number; // 最大化目标值（infeasible 时为 -Infinity，unbounded 时为 Infinity）
  x: number[];
}

export function solveLpMax(A: number[][], b: number[], c: number[]): LpResult {
  const m = b.length;
  const n = c.length;
  const N = new Array<number>(n + 1);
  const B = new Array<number>(m);
  const D: number[][] = Array.from({ length: m + 2 }, () => new Array<number>(n + 2).fill(0));

  for (let i = 0; i < m; i++) {
    for (let j = 0; j < n; j++) D[i]![j] = A[i]![j]!;
  }
  for (let i = 0; i < m; i++) {
    B[i] = n + i;
    D[i]![n] = -1;
    D[i]![n + 1] = b[i]!;
  }
  for (let j = 0; j < n; j++) {
    N[j] = j;
    D[m]![j] = -c[j]!;
  }
  N[n] = -1;
  D[m + 1]![n] = 1;

  const pivot = (r: number, s: number): void => {
    const inv = 1 / D[r]![s]!;
    for (let i = 0; i < m + 2; i++) {
      if (i === r || Math.abs(D[i]![s]!) <= EPS) continue;
      const inv2 = D[i]![s]! * inv;
      for (let j = 0; j < n + 2; j++) D[i]![j] = D[i]![j]! - D[r]![j]! * inv2;
      D[i]![s] = D[r]![s]! * inv2;
    }
    for (let j = 0; j < n + 2; j++) if (j !== s) D[r]![j] = D[r]![j]! * inv;
    for (let i = 0; i < m + 2; i++) if (i !== r) D[i]![s] = D[i]![s]! * -inv;
    D[r]![s] = inv;
    const tmp = B[r]!;
    B[r] = N[s]!;
    N[s] = tmp;
  };

  const simplex = (phase: number): boolean => {
    const x = m + phase - 1;
    for (;;) {
      let s = -1;
      for (let j = 0; j <= n; j++) {
        if (N[j] === -phase) continue;
        if (
          s === -1 ||
          D[x]![j]! < D[x]![s]! - EPS ||
          (Math.abs(D[x]![j]! - D[x]![s]!) <= EPS && N[j]! < N[s]!)
        ) {
          s = j;
        }
      }
      if (D[x]![s]! >= -EPS) return true;
      let r = -1;
      for (let i = 0; i < m; i++) {
        if (D[i]![s]! <= EPS) continue;
        if (r === -1) {
          r = i;
          continue;
        }
        const lhs = D[i]![n + 1]! / D[i]![s]!;
        const rhs = D[r]![n + 1]! / D[r]![s]!;
        if (lhs < rhs - EPS || (Math.abs(lhs - rhs) <= EPS && B[i]! < B[r]!)) r = i;
      }
      if (r === -1) return false;
      pivot(r, s);
    }
  };

  if (m > 0) {
    let r = 0;
    for (let i = 1; i < m; i++) if (D[i]![n + 1]! < D[r]![n + 1]!) r = i;
    if (D[r]![n + 1]! < -EPS) {
      pivot(r, n);
      if (!simplex(2) || D[m + 1]![n + 1]! < -EPS) {
        return { status: 'infeasible', objective: -Infinity, x: [] };
      }
      for (let i = 0; i < m; i++) {
        if (B[i] === -1) {
          let s = 0;
          for (let j = 1; j <= n; j++) {
            if (D[i]![j]! < D[i]![s]! - EPS || (Math.abs(D[i]![j]! - D[i]![s]!) <= EPS && N[j]! < N[s]!)) s = j;
          }
          pivot(i, s);
        }
      }
    }
  }

  const ok = simplex(1);
  const x = new Array<number>(n).fill(0);
  for (let i = 0; i < m; i++) if (B[i]! < n) x[B[i]!] = D[i]![n + 1]!;
  if (!ok) return { status: 'unbounded', objective: Infinity, x };
  return { status: 'optimal', objective: D[m]![n + 1]!, x };
}

/** 最小化版本：minimize c·x, s.t. A x <= b, x >= 0。 */
export function solveLpMin(A: number[][], b: number[], c: number[]): LpResult {
  const negC = c.map((v) => -v);
  const res = solveLpMax(A, b, negC);
  if (res.status === 'optimal') res.objective = -res.objective;
  if (res.status === 'infeasible') res.objective = Infinity;
  if (res.status === 'unbounded') res.objective = -Infinity;
  return res;
}
