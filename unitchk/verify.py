"""独立验证：不复用求解器内部状态，从原始输入重新核对。

- verify_substitution：把解代入全部原始约束逐条重算；
- verify_kernel：核对特解满足 A x = b、格基满足 A v = 0；
- verify_certificate：核对矛盾证书 c·A = 0 且 c·b ≠ 0；
- check_infeasibility：独立复现整除性失败（含 det(U)=±1、A·U=H）；
- verify_cycle：重算矛盾环复合映射，确认 ≠ 恒等。
"""
from __future__ import annotations

from .constraints import derive_form
from .graph import AffineMap
from .steps import StepLog


def verify_substitution(system, assignment) -> list[str]:
    """assignment: {(unit, dim_index): int}。逐条重算原始方程两边，返回错误列表。"""
    errors = []
    d = len(system.registry.dimensions)
    unknowns = set(system.unknowns)
    for eq in system.equations:
        lf = derive_form(eq.lhs, system.registry, unknowns, StepLog())
        rf = derive_form(eq.rhs, system.registry, unknowns, StepLog())
        lv = [lf.const[j] + sum(c * assignment.get((u, j), 0)
                                for (u, jj), c in lf.coeffs.items() if jj == j)
              for j in range(d)]
        rv = [rf.const[j] + sum(c * assignment.get((u, j), 0)
                                for (u, jj), c in rf.coeffs.items() if jj == j)
              for j in range(d)]
        if lv != rv:
            errors.append(f"方程 {eq.label or '?'} 代入后不成立: {lv} != {rv}")
    return errors


def verify_kernel(system, solution) -> list[str]:
    """独立核对：A·特解 == b 且每个格基向量满足 A·v == 0。"""
    errors = []
    A, b = system.A, system.b
    n = len(solution.particular)
    for i, row in enumerate(A):
        lhs = sum(row[j] * solution.particular[j] for j in range(n))
        if lhs != b[i]:
            errors.append(f"特解不满足第 {i} 行: {lhs} != {b[i]}")
    for k, vec in enumerate(solution.lattice):
        for i, row in enumerate(A):
            v = sum(row[j] * vec[j] for j in range(n))
            if v != 0:
                errors.append(f"格基 {k} 不在核中: 第 {i} 行点积 = {v}")
    return errors


def verify_certificate(A, b, cert) -> bool:
    """核对整数证书：c·A == 0 且 c·b != 0。"""
    m, n = len(A), len(A[0]) if A else 0
    if len(cert) != m:
        return False
    for j in range(n):
        if sum(cert[i] * A[i][j] for i in range(m)) != 0:
            return False
    return sum(cert[i] * b[i] for i in range(m)) != 0


def det_bareiss(M) -> int:
    """Bareiss 无分数算法精确计算整数矩阵行列式。"""
    n = len(M)
    if n == 0:
        return 1
    A = [list(row) for row in M]
    sign = 1
    prev = 1
    for k in range(n - 1):
        if A[k][k] == 0:
            swap = None
            for i in range(k + 1, n):
                if A[i][k] != 0:
                    swap = i
                    break
            if swap is None:
                return 0
            A[k], A[swap] = A[swap], A[k]
            sign = -sign
        pivot = A[k][k]
        for i in range(k + 1, n):
            for j in range(k + 1, n):
                A[i][j] = (A[i][j] * pivot - A[i][k] * A[k][j]) // prev
            A[i][k] = 0
        prev = pivot
    return sign * A[n - 1][n - 1]


def check_infeasibility(A, b, evidence) -> bool:
    """独立验证不相容证据，不调用求解器。"""
    if evidence.get("type") == "contradiction":
        return verify_certificate(A, b, evidence["certificate"])
    if evidence.get("type") != "divisibility":
        return False
    U, H = evidence["U"], evidence["H"]
    perm, piv = evidence["perm"], evidence["pivot_cols"]
    m = len(A)
    n = len(A[0]) if A else 0
    if det_bareiss(U) not in (1, -1):
        return False
    for i in range(m):
        for j in range(n):
            if sum(A[i][k] * U[k][j] for k in range(n)) != H[i][j]:
                return False
    bp = [b[perm[i]] for i in range(m)]
    y = {}
    for s, col in enumerate(piv):
        rhs = bp[s] - sum(H[s][c] * y[c] for c in piv[:s])
        if rhs % H[s][col] != 0:
            return (evidence["pivot_column"] == col
                    and evidence["pivot"] == H[s][col]
                    and evidence["rhs"] == rhs)
        y[col] = rhs // H[s][col]
    return False  # 未能复现整除失败，证据无效


def compose_path(amaps) -> AffineMap:
    total = AffineMap()
    for m in amaps:
        total = total.then(m)
    return total


def verify_cycle(cycle_edges) -> AffineMap:
    """独立重算矛盾环的复合映射（调用方确认其 ≠ 恒等）。"""
    return compose_path([e.amap for e in cycle_edges])
