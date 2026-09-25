"""自写稠密线性代数（仅标准库），用于小规模核验 GMRES 结果。"""

from __future__ import annotations

import math
from typing import List, Sequence

__all__ = ["DenseMatrix", "dense_solve"]


class DenseMatrix:
    """稠密方阵，提供符合 gmres 约定的 matvec(x, n) 回调。"""

    def __init__(self, rows: Sequence[Sequence[float]]) -> None:
        self.rows: List[List[float]] = [list(map(float, row)) for row in rows]
        self.n = len(self.rows)
        if self.n == 0:
            raise ValueError("矩阵至少为 1 阶")
        for i, row in enumerate(self.rows):
            if len(row) != self.n:
                raise ValueError(
                    f"第 {i} 行长度 {len(row)} 与矩阵阶数 {self.n} 不一致"
                )
            for j, v in enumerate(row):
                if not math.isfinite(v):
                    raise ValueError(f"矩阵元素 ({i}, {j})={v!r} 非有限")

    def matvec(self, x: Sequence[float], n: int) -> List[float]:
        if n != self.n:
            raise ValueError(f"显式尺寸 n={n} 与矩阵阶数 {self.n} 不一致")
        if len(x) != n:
            raise ValueError(f"向量长度 {len(x)} 与 n={n} 不一致")
        return [
            math.fsum(row[j] * x[j] for j in range(n)) for row in self.rows
        ]


def dense_solve(
    a: Sequence[Sequence[float]], b: Sequence[float]
) -> List[float]:
    """高斯消元 + 部分主元求解 A x = b；奇异时抛 ValueError。"""
    n = len(a)
    if n == 0:
        raise ValueError("矩阵至少为 1 阶")
    if len(b) != n:
        raise ValueError(f"右端长度 {len(b)} 与矩阵阶数 {n} 不一致")
    m = [list(map(float, row)) + [float(bi)] for row, bi in zip(a, b)]
    for i, row in enumerate(m):
        if len(row) != n + 1:
            raise ValueError(f"第 {i} 行长度与矩阵阶数 {n} 不一致")

    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(m[r][col]))
        if m[piv][col] == 0.0:
            raise ValueError(f"矩阵在第 {col} 列奇异，无法求解")
        m[col], m[piv] = m[piv], m[col]
        for r in range(col + 1, n):
            factor = m[r][col] / m[col][col]
            if factor != 0.0:
                for c in range(col, n + 1):
                    m[r][c] -= factor * m[col][c]

    x = [0.0] * n
    for r in range(n - 1, -1, -1):
        s = m[r][n] - math.fsum(m[r][c] * x[c] for c in range(r + 1, n))
        x[r] = s / m[r][r]
    return x
