/**
 * 精确单纯形法求解器：
 *   max  c·x
 *   s.t. A x <= b, x >= 0
 *
 * - 全部运算在 BigInt 有理数上进行，结果精确（非浮点近似）。
 * - 两阶段法处理负 b / 不可行；Bland 最小指标规则保证不发生循环。
 * - 返回最优 / 不可行 / 无界 / 迭代预算耗尽，并附带可独立验证的证书。
 */

import type { Rat } from "./rational.ts";
import {
  ZERO,
  ONE,
  add,
  sub,
  mul,
  div,
  neg,
  cmp,
  isZero,
  isPositive,
  isNegative,
} from "./rational.ts";

export interface LP {
  c: Rat[]; // 长度 n
  A: Rat[][]; // m 行，每行长度 n
  b: Rat[]; // 长度 m
}

export function validateLP(lp: LP): void {
  const n = lp.c.length;
  if (lp.A.length !== lp.b.length) {
    throw new Error(`dimension mismatch: A has ${lp.A.length} rows but b has ${lp.b.length}`);
  }
  lp.A.forEach((row, i) => {
    if (row.length !== n) {
      throw new Error(`dimension mismatch: A[${i}] has ${row.length} entries, expected ${n}`);
    }
  });
}

export type Status = "optimal" | "infeasible" | "unbounded" | "iteration_limit";

export interface SolveResult {
  status: Status;
  /** 实际执行的枢轴迭代次数（两阶段合计）。 */
  iterations: number;
  /** status = optimal 时：最优值 c·x。 */
  objective?: Rat;
  /** status = optimal | unbounded 时：原始可行点。 */
  x?: Rat[];
  /** status = optimal 时：对偶可行解 y >= 0，Aᵀy >= c，b·y = c·x。 */
  y?: Rat[];
  /** status = infeasible 时：Farkas 证书 y >= 0，Aᵀy >= 0，b·y < 0。 */
  // 复用字段 y。
  /** status = unbounded 时：射线方向 d >= 0，A d <= 0，c·d > 0。 */
  direction?: Rat[];
  /** status = optimal 时：是否存在（可构造的）其他最优顶点。 */
  hasAlternativeOptima?: boolean;
}

/** 默认枢轴迭代预算。 */
export const DEFAULT_BUDGET = 1_000_000;

interface Dict {
  B: number[]; // 基变量下标，长度 m
  N: number[]; // 非基变量下标
  T: Rat[][]; // T[i][j]：第 i 个基变量表达式中非基变量 N[j] 的系数（字典取负号约定）
  d: Rat[]; // d[i]：第 i 个基变量的常数项（即当前取值，可行性不变式 d >= 0）
  v: Rat; // 目标函数常数项
  w: Rat[]; // w[j]：目标函数中非基变量 N[j] 的既约费用
}

/**
 * 以 (行 r, 列 p) 为主元做一次精确枢轴。
 * 字典约定：x_{B[i]} = d[i] - Σ_j T[i][j]·x_{N[j]}，z = v + Σ_j w[j]·x_{N[j]}。
 */
function pivot(D: Dict, r: number, p: number): void {
  const t = D.T[r][p];
  if (isZero(t)) throw new Error("pivot on zero coefficient");
  const inv = div(ONE, t);
  const width = D.N.length;

  // 新主元行：x_e = d' - Σ T' x（含新非基变量 = 旧基变量）
  const newRow: Rat[] = new Array(width);
  for (let j = 0; j < width; j++) {
    newRow[j] = j === p ? inv : mul(D.T[r][j], inv);
  }
  const newD = mul(D.d[r], inv);

  // 其余行：先把第 p 列清零，再统一按 T'[i][j] = T[i][j] - T[i][p]·newRow[j] 更新
  for (let i = 0; i < D.B.length; i++) {
    if (i === r) continue;
    const factor = D.T[i][p];
    if (isZero(factor)) continue;
    D.d[i] = sub(D.d[i], mul(factor, newD));
    D.T[i][p] = ZERO;
    for (let j = 0; j < width; j++) {
      D.T[i][j] = sub(D.T[i][j], mul(factor, newRow[j]));
    }
  }

  // 目标行同理
  const wp = D.w[p];
  if (!isZero(wp)) {
    D.v = add(D.v, mul(wp, newD));
    D.w[p] = ZERO;
    for (let j = 0; j < width; j++) {
      D.w[j] = sub(D.w[j], mul(wp, newRow[j]));
    }
  }

  D.T[r] = newRow;
  D.d[r] = newD;
  const leaving = D.B[r];
  D.B[r] = D.N[p];
  D.N[p] = leaving;
}

/** Bland 进基规则：既约费用为正的非基变量中，变量下标最小者。返回列位置，无则 -1。 */
function blandEntering(D: Dict): number {
  let best = -1;
  for (let j = 0; j < D.N.length; j++) {
    if (isPositive(D.w[j]) && (best < 0 || D.N[j] < D.N[best])) best = j;
  }
  return best;
}

/** Bland 离基规则：最小比值测试，平局时取基变量下标最小者。返回行位置，无阻挡则 -1。 */
function blandLeaving(D: Dict, p: number): number {
  let best = -1;
  let bestRatio: Rat = ZERO;
  for (let i = 0; i < D.B.length; i++) {
    if (!isPositive(D.T[i][p])) continue;
    const ratio = div(D.d[i], D.T[i][p]);
    if (
      best < 0 ||
      cmp(ratio, bestRatio) < 0 ||
      (cmp(ratio, bestRatio) === 0 && D.B[i] < D.B[best])
    ) {
      best = i;
      bestRatio = ratio;
    }
  }
  return best;
}

/** 从当前字典提取原始变量取值（基本可行解）。 */
function extractPrimal(D: Dict, n: number): Rat[] {
  const x: Rat[] = new Array(n).fill(ZERO);
  for (let i = 0; i < D.B.length; i++) {
    if (D.B[i] < n) x[D.B[i]] = D.d[i];
  }
  return x;
}

/** 从目标行提取对偶解：y_i = -（松弛变量 s_i 的既约费用），基变量的既约费用视为 0。 */
function extractDuals(D: Dict, n: number, m: number): Rat[] {
  const y: Rat[] = new Array(m).fill(ZERO);
  for (let j = 0; j < D.N.length; j++) {
    const id = D.N[j];
    if (id >= n && id < n + m) y[id - n] = neg(D.w[j]);
  }
  return y;
}

/**
 * 多重最优检测（保守、可靠）：若存在既约费用为 0 的原始非基变量，
 * 且其最小比值严格为正，则沿它移动可到达另一个最优顶点。
 */
function detectAlternativeOptima(D: Dict, n: number): boolean {
  for (let p = 0; p < D.N.length; p++) {
    if (D.N[p] >= n || !isZero(D.w[p])) continue;
    let hasBlocking = false;
    let allPositive = true;
    for (let i = 0; i < D.B.length; i++) {
      if (!isPositive(D.T[i][p])) continue;
      hasBlocking = true;
      if (!isPositive(D.d[i])) allPositive = false;
    }
    // 无阻挡行：沿该变量可无限移动且目标不变（最优面无界）⟹ 多重最优
    if (!hasBlocking) return true;
    // 最小比值 > 0 ⟺ 所有阻挡行 d[i] > 0；此时进基产生不同的最优顶点
    if (allPositive) return true;
  }
  return false;
}

/**
 * 求解 max c·x, A x <= b, x >= 0。
 * @param budget 最大枢轴迭代次数；耗尽时返回 iteration_limit（绝不伪报不可行）。
 */
export function solve(lp: LP, budget: number = DEFAULT_BUDGET): SolveResult {
  validateLP(lp);
  const n = lp.c.length;
  const m = lp.b.length;
  const aux = n + m; // 辅助变量 x0 的下标

  const D: Dict = {
    B: Array.from({ length: m }, (_, i) => n + i), // 松弛变量 s_i 初始为基
    N: Array.from({ length: n }, (_, j) => j),
    T: lp.A.map((row) => row.slice()),
    d: lp.b.slice(),
    v: ZERO,
    w: lp.c.slice(),
  };

  let iterations = 0;
  const doPivot = (r: number, p: number): boolean => {
    if (iterations >= budget) return false;
    pivot(D, r, p);
    iterations++;
    return true;
  };
  const limit = (): SolveResult => ({ status: "iteration_limit", iterations });

  // ---------- 第一阶段：存在负 b 时引入辅助变量 x0，最大化 -x0 ----------
  if (D.d.some(isNegative)) {
    D.N.push(aux);
    for (let i = 0; i < m; i++) D.T[i].push(neg(ONE)); // s_i = b_i - Σ a_ij x_j + x0
    D.w = D.N.map((id) => (id === aux ? neg(ONE) : ZERO));
    D.v = ZERO;

    // 初始枢轴：x0 进基，b 最小的行离基，使字典变为可行
    let r = 0;
    for (let i = 1; i < m; i++) {
      const c = cmp(D.d[i], D.d[r]);
      if (c < 0 || (c === 0 && D.B[i] < D.B[r])) r = i;
    }
    if (!doPivot(r, D.N.indexOf(aux))) return limit();

    for (;;) {
      const p = blandEntering(D);
      if (p < 0) break;
      const rr = blandLeaving(D, p);
      if (rr < 0) throw new Error("phase 1 unbounded (should not happen)");
      if (!doPivot(rr, p)) return limit();
    }

    if (isNegative(D.v)) {
      // max -x0 < 0 ⟺ 原问题不可行；目标行给出 Farkas 证书
      return { status: "infeasible", iterations, y: extractDuals(D, n, m) };
    }

    // x0 = 0：若 x0 仍在基中（退化），用一次退化枢轴把它换出；整行为零则约束冗余，删行
    const bi = D.B.indexOf(aux);
    if (bi >= 0) {
      let p = -1;
      for (let j = 0; j < D.N.length; j++) {
        if (D.N[j] === aux || isZero(D.T[bi][j])) continue;
        if (p < 0 || D.N[j] < D.N[p]) p = j;
      }
      if (p >= 0) {
        if (!doPivot(bi, p)) return limit();
      } else {
        D.B.splice(bi, 1);
        D.T.splice(bi, 1);
        D.d.splice(bi, 1);
      }
    }

    // 删除 x0 列
    const ap = D.N.indexOf(aux);
    if (ap >= 0) {
      D.N.splice(ap, 1);
      for (const row of D.T) row.splice(ap, 1);
      D.w.splice(ap, 1);
    }

    // 以当前字典重建原目标函数
    D.v = ZERO;
    D.w = D.N.map((id) => (id < n ? lp.c[id] : ZERO));
    for (let i = 0; i < D.B.length; i++) {
      const id = D.B[i];
      if (id >= n || isZero(lp.c[id])) continue;
      D.v = add(D.v, mul(lp.c[id], D.d[i]));
      for (let j = 0; j < D.N.length; j++) {
        D.w[j] = sub(D.w[j], mul(lp.c[id], D.T[i][j]));
      }
    }
  }

  // ---------- 第二阶段：Bland 规则单纯形 ----------
  for (;;) {
    const p = blandEntering(D);
    if (p < 0) {
      return {
        status: "optimal",
        iterations,
        objective: D.v,
        x: extractPrimal(D, n),
        y: extractDuals(D, n, m),
        hasAlternativeOptima: detectAlternativeOptima(D, n),
      };
    }
    const r = blandLeaving(D, p);
    if (r < 0) {
      // 无界：构造射线方向 d
      const direction: Rat[] = new Array(n).fill(ZERO);
      const enteringVar = D.N[p];
      if (enteringVar < n) direction[enteringVar] = ONE;
      for (let i = 0; i < D.B.length; i++) {
        if (D.B[i] < n) direction[D.B[i]] = neg(D.T[i][p]); // T[i][p] <= 0 ⟹ 分量 >= 0
      }
      return { status: "unbounded", iterations, x: extractPrimal(D, n), direction };
    }
    if (!doPivot(r, p)) return limit();
  }
}
