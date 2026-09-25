"""有理约束枚举对照器：穷举顶点，独立验证 LP 结果。

把系统统一写成 A x <= b（等式折成两个不等式），枚举所有
n 行子集解线性方程组，检验可行性后收集顶点，再对目标取
min/max。仅用于小规模对照测试与演示，复杂度为组合级。
"""

from __future__ import annotations

from fractions import Fraction
from itertools import combinations
from typing import Optional, Sequence

from .system import System


def _solve_dense(matrix: list[list[Fraction]], rhs: list[Fraction]) -> Optional[list[Fraction]]:
    """精确高斯消元解 n x n 线性系统；奇异返回 None。"""
    n = len(matrix)
    aug = [row[:] + [b] for row, b in zip(matrix, rhs, strict=True)]
    for col in range(n):
        pivot = None
        for r in range(col, n):
            if aug[r][col] != 0:
                pivot = r
                break
        if pivot is None:
            return None
        aug[col], aug[pivot] = aug[pivot], aug[col]
        pv = aug[col][col]
        aug[col] = [v / pv for v in aug[col]]
        for r in range(n):
            if r != col and aug[r][col] != 0:
                factor = aug[r][col]
                aug[r] = [a - factor * b for a, b in zip(aug[r], aug[col], strict=True)]
    return [aug[i][-1] for i in range(n)]


def enumerate_vertices(system: System) -> list[list[Fraction]]:
    """枚举可行多面体的全部顶点（去重）。"""
    rows: list[list[Fraction]] = []
    rhs: list[Fraction] = []
    for row, b in zip(system.a_eq, system.b_eq, strict=True):
        rows.append(list(row))
        rhs.append(b)
        rows.append([-a for a in row])
        rhs.append(-b)
    for row, b in zip(system.a_ub, system.b_ub, strict=True):
        rows.append(list(row))
        rhs.append(b)

    n = system.n_vars
    vertices: dict[tuple[Fraction, ...], list[Fraction]] = {}
    if len(rows) < n:
        return []
    for combo in combinations(range(len(rows)), n):
        matrix = [rows[i] for i in combo]
        vec = [rhs[i] for i in combo]
        sol = _solve_dense(matrix, vec)
        if sol is None:
            continue
        ok = True
        for row, b in zip(rows, rhs, strict=True):
            if sum(a * x for a, x in zip(row, sol, strict=True)) > b:
                ok = False
                break
        if ok:
            vertices[tuple(sol)] = sol
    return list(vertices.values())


def enum_bounds(
    system: System, objective: Sequence[Fraction]
) -> Optional[tuple[Fraction, Fraction]]:
    """由全部顶点推出目标的 (下界, 上界)；无可行顶点返回 None。"""
    vertices = enumerate_vertices(system)
    if not vertices:
        return None
    values = [
        sum(c * x for c, x in zip(objective, vertex, strict=True))
        for vertex in vertices
    ]
    return min(values), max(values)
