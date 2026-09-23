"""整数线性系统 A x = b 的精确求解（不使用任何浮点近似）。

方法：对 A 做列幺模变换（列交换、列取负、列的整数倍相加），
得到 A·U = H，其中 H 的非零列构成下三角梯形，零列对应自由变量。
令 x = U·y，则 A x = b 等价于 H y = b：
- 前代回代时若 pivot 不能整除右端，则系统整数不相容（整除性证据）；
- 秩之外的行若残差非零，则系统有理不相容，并给出整数证书 c
  （c·A = 0 而 c·b ≠ 0），可被独立验证；
- 否则 x0 = U·y 为特解，U 的零列张成整数解格。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import lcm

from .steps import Step, StepLog


@dataclass
class Solution:
    particular: list[int]        # 特解 x0（整数）
    lattice: list[list[int]]     # 整数解格的基（齐次解方向）
    var_names: list[str]
    free_columns: list[int]
    steps: list[Step]

    @property
    def free_variables(self) -> list[str]:
        return [self.var_names[c] for c in self.free_columns]

    def assignment(self, params=None) -> dict:
        """代入格参数（默认全 0 即特解），返回 变量名 -> 整数值。"""
        x = list(self.particular)
        for k, vec in enumerate(self.lattice):
            t = 0 if params is None else params[k]
            for i in range(len(x)):
                x[i] += t * vec[i]
        return {name: x[i] for i, name in enumerate(self.var_names)}


@dataclass
class Infeasible:
    kind: str            # "contradiction"（有理不相容）| "divisibility"（整数不相容）
    evidence: dict       # 可被 verify.check_infeasibility 独立验证的证据
    steps: list[Step]


def solve_integer_system(A, b, var_names=(), row_labels=()):
    """精确求解 A x = b（x 为整数向量）。返回 Solution 或 Infeasible。"""
    log = StepLog()
    m = len(A)
    n = len(A[0]) if A else len(var_names)
    var_names = list(var_names) or [f"x{i}" for i in range(n)]
    row_labels = list(row_labels) or [f"row{i}" for i in range(m)]

    if n == 0:
        for i in range(m):
            if b[i] != 0:
                cert = [0] * m
                cert[i] = 1
                return Infeasible("contradiction", {
                    "type": "contradiction",
                    "equation": row_labels[i],
                    "certificate": cert,
                    "certificate_rhs": b[i],
                    "residual": b[i],
                    "detail": f"空系统存在 0 = {b[i]} 的矛盾",
                }, log.steps)
        return Solution([], [], var_names, [], log.steps)

    H = [list(row) for row in A]
    U = [[1 if i == j else 0 for j in range(n)] for i in range(n)]
    perm = list(range(m))  # perm[新位置] = 原始行号
    pivot_cols: list[int] = []
    r = 0
    for col in range(n):
        if r >= m:
            break
        piv = None
        for i in range(r, m):
            if H[i][col] != 0:
                piv = i
                break
        if piv is None:
            log.record("skip-column",
                       "零列：该变量不受约束，成为解格的自由方向",
                       f"列 {col} ({var_names[col]})")
            continue
        if piv != r:
            H[r], H[piv] = H[piv], H[r]
            perm[r], perm[piv] = perm[piv], perm[r]
            log.record("row-swap", "方程重排：解集不变", f"行 {piv} <-> 行 {r}")
        # 欧几里得列消元：把第 r 行 pivot 右侧消为零
        while True:
            ks = [k for k in range(col + 1, n) if H[r][k] != 0]
            if not ks:
                break
            k = min(ks, key=lambda kk: abs(H[r][kk]))
            if abs(H[r][k]) < abs(H[r][col]):
                for i in range(m):
                    H[i][col], H[i][k] = H[i][k], H[i][col]
                for i in range(n):
                    U[i][col], U[i][k] = U[i][k], U[i][col]
                log.record("col-swap",
                           "变量重命名：幺模变换，整数解集保持",
                           f"列 {col} <-> 列 {k}")
            else:
                q = H[r][k] // H[r][col]
                for i in range(m):
                    H[i][k] -= q * H[i][col]
                for i in range(n):
                    U[i][k] -= q * U[i][col]
                log.record("col-reduce",
                           "列 k <- 列 k - q*列 p：Z 上幺模（逆变换为 k <- k + q*p），整数解集保持",
                           f"k={k}, p={col}, q={q}")
        if H[r][col] < 0:
            for i in range(m):
                H[i][col] = -H[i][col]
            for i in range(n):
                U[i][col] = -U[i][col]
            log.record("col-negate", "列取负：幺模变换，整数解集保持", f"列 {col}")
        pivot_cols.append(col)
        r += 1
    rank = r
    log.record("elimination-done",
               "列幺模变换结束：A·U = H，H 的非零列下三角、零列对应自由变量",
               f"秩 = {rank}，自由列 = {n - rank}")

    bp = [b[perm[i]] for i in range(m)]
    y = [0] * n
    for s, col in enumerate(pivot_cols):
        rhs = bp[s]
        for c in pivot_cols[:s]:
            rhs -= H[s][c] * y[c]
        pivval = H[s][col]
        if rhs % pivval != 0:
            log.record("infeasible",
                       "整除性判据：pivot 方程在 Z 上要求 pivot 整除右端",
                       f"{pivval} 不整除 {rhs}")
            return Infeasible("divisibility", {
                "type": "divisibility",
                "equation": row_labels[perm[s]],
                "pivot_column": col,
                "pivot": pivval,
                "rhs": rhs,
                "U": [row[:] for row in U],
                "perm": list(perm),
                "pivot_cols": list(pivot_cols),
                "H": [row[:] for row in H],
                "detail": f"方程 {row_labels[perm[s]]} 要求 {pivval} | {rhs}，整数整除失败",
            }, log.steps)
        y[col] = rhs // pivval
        log.record("substitute",
                   "前代：已解变量精确回代（整数除法，余数为零）",
                   f"y[{col}] = {y[col]}")

    for i in range(rank, m):
        resid = bp[i]
        for c in pivot_cols:
            resid -= H[i][c] * y[c]
        if resid != 0:
            # 构造整数证书 c：c·A = 0 且 c·b != 0
            w = [Fraction(0)] * rank
            for t in range(rank):
                acc = Fraction(H[i][pivot_cols[t]])
                for s in range(t):
                    acc -= w[s] * H[s][pivot_cols[t]]
                w[t] = acc / H[t][pivot_cols[t]]
            c = [Fraction(0)] * m
            c[perm[i]] = 1
            for s in range(rank):
                c[perm[s]] -= w[s]
            den = 1
            for x in c:
                den = lcm(den, x.denominator)
            cert = [int(x * den) for x in c]
            cert_rhs = sum(cert[k] * b[k] for k in range(m))
            log.record("infeasible",
                       "矛盾判据：存在方程的整数组合使左端为 0 而右端非零",
                       f"证书右端 = {cert_rhs}")
            return Infeasible("contradiction", {
                "type": "contradiction",
                "equation": row_labels[perm[i]],
                "certificate": cert,
                "certificate_rhs": cert_rhs,
                "residual": resid,
                "detail": f"方程 {row_labels[perm[i]]} 残差 {resid}；"
                          f"整数证书 c 满足 c·A = 0 且 c·b = {cert_rhs} ≠ 0",
            }, log.steps)

    particular = [sum(U[i][k] * y[k] for k in range(n)) for i in range(n)]
    free_cols = [c for c in range(n) if c not in pivot_cols]
    lattice = [[U[i][f] for i in range(n)] for f in free_cols]
    log.record("solution",
               "解 = 特解 + 整数解格；格基为 U 的零列（A·U 的零列，严格在核中）",
               f"格维数 = {len(free_cols)}")
    return Solution(particular, lattice, var_names, free_cols, log.steps)
