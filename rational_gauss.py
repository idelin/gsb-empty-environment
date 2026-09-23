"""有理数域 Q 上的精确高斯消元。

设计约束
--------
- 仅使用标准库 ``fractions.Fraction``，全程精确有理运算，绝不转浮点；
  输入为 ``int`` / ``Fraction`` / 形如 "3/2" 的字符串，显式拒绝 ``float``。
- 记录完整的行/列操作轨迹，每步注明该初等变换的适用域。
- 求矩阵的秩、线性方程组 Ax = b 的解空间（特解 + 零空间基）。
- 不相容系统给出 "0 = c (c != 0)" 的显式证据行及秩判据。
- 列交换视为变量重命名：记录置换，求解后还原原始变量顺序。

Python >= 3.11，仅依赖标准库。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Sequence

DOMAIN_FIELD = "域 Q（特征 0，Fraction 精确运算，无浮点舍入）"
DOMAIN_RING = "任意交换环（仅涉及加法与乘法，无需除法）"
DOMAIN_UNIT = "任意域（缩放因子非零即为单位，可逆）"
DOMAIN_PERM = "任意环（置换不改变行生成的子模）"
DOMAIN_COLPERM = "任意环（列交换 = 变量重命名，须记录置换以还原变量顺序）"


def to_fraction(value) -> Fraction:
    """把 int / Fraction / 精确字符串转为 Fraction；拒绝 float 以杜绝浮点污染。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):  # bool 是 int 子类，单独拒绝以免歧义
        raise TypeError(f"不接受的类型 bool: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)  # "3/2"、"-4"、"0.125" 均为精确解析
    raise TypeError(
        f"不接受的类型 {type(value).__name__}: {value!r}；"
        "请使用 int、Fraction 或精确字符串（如 '3/2'），禁止 float"
    )


def to_matrix(rows: Sequence[Sequence]) -> list[list[Fraction]]:
    matrix = [[to_fraction(v) for v in row] for row in rows]
    if matrix:
        width = len(matrix[0])
        if any(len(row) != width for row in matrix):
            raise ValueError("矩阵各行长度不一致")
    return matrix


@dataclass
class Operation:
    """一步初等变换及其适用域说明。"""

    kind: str          # 'row_swap' | 'row_scale' | 'row_add' | 'col_swap'
    description: str   # 人类可读的变换描述
    domain: str        # 该变换的适用域说明

    def __str__(self) -> str:
        return f"{self.description}\n         适用域: {self.domain}"


@dataclass
class RREFResult:
    """约化行阶梯形（RREF）结果。"""

    matrix: list[list[Fraction]]
    pivot_columns: list[int]        # 主元列（列交换后的新坐标）
    rank: int
    operations: list[Operation]
    column_permutation: list[int]   # column_permutation[新列] = 原列


def rref(
    matrix: Sequence[Sequence],
    *,
    column_pivoting: bool = False,
    column_swap_bound: int | None = None,
) -> RREFResult:
    """对矩阵做精确约化行阶梯形消元，返回结果与操作轨迹。

    column_pivoting: 当当前主元列全为零时，允许与后面的列交换
                     （等价于变量重命名，置换会被记录）。
    column_swap_bound: 列交换只允许发生在前 column_swap_bound 列之内
                       （用于保护增广列不参与交换）。
    """
    m = to_matrix(matrix)
    rows = len(m)
    cols = len(m[0]) if rows else 0
    if column_swap_bound is None:
        column_swap_bound = cols
    ops: list[Operation] = []
    perm = list(range(cols))
    pivots: list[int] = []
    r = 0
    c = 0
    while r < rows and c < cols:
        pivot_row = next((i for i in range(r, rows) if m[i][c] != 0), None)
        if pivot_row is None:
            if column_pivoting and c < column_swap_bound:
                swap_c = next(
                    (
                        j
                        for j in range(c + 1, column_swap_bound)
                        if any(m[i][j] != 0 for i in range(r, rows))
                    ),
                    None,
                )
                if swap_c is None:
                    break  # 第 r 行起全为零，消元结束
                for row in m:
                    row[c], row[swap_c] = row[swap_c], row[c]
                perm[c], perm[swap_c] = perm[swap_c], perm[c]
                ops.append(
                    Operation(
                        "col_swap",
                        f"列交换: C{c + 1} <-> C{swap_c + 1}"
                        f"（变量 x{perm[c] + 1} <-> x{perm[swap_c] + 1}，已记录置换）",
                        DOMAIN_COLPERM,
                    )
                )
                pivot_row = next(i for i in range(r, rows) if m[i][c] != 0)
            else:
                c += 1
                continue
        if pivot_row != r:
            m[r], m[pivot_row] = m[pivot_row], m[r]
            ops.append(
                Operation(
                    "row_swap",
                    f"行交换: R{r + 1} <-> R{pivot_row + 1}",
                    DOMAIN_PERM,
                )
            )
        pivot = m[r][c]
        if pivot != 1:
            inv = 1 / pivot
            for j in range(cols):
                m[r][j] *= inv
            ops.append(
                Operation(
                    "row_scale",
                    f"行缩放: R{r + 1} <- R{r + 1} / ({pivot})",
                    DOMAIN_UNIT,
                )
            )
        for i in range(rows):
            if i != r and m[i][c] != 0:
                factor = m[i][c]
                for j in range(cols):
                    m[i][j] -= factor * m[r][j]
                ops.append(
                    Operation(
                        "row_add",
                        f"行倍加: R{i + 1} <- R{i + 1} - ({factor}) * R{r + 1}",
                        DOMAIN_RING,
                    )
                )
        pivots.append(c)
        r += 1
        c += 1
    return RREFResult(m, pivots, len(pivots), ops, perm)


def rank(matrix: Sequence[Sequence]) -> int:
    """矩阵的秩（域 Q 上精确计算）。"""
    return rref(matrix).rank


@dataclass
class Solution:
    """线性方程组 Ax = b 的求解结果（变量均已还原为原始顺序）。"""

    consistent: bool
    rank_a: int                      # rank(A)
    rank_augmented: int              # rank([A|b])
    num_variables: int
    particular: list[Fraction] | None          # 特解（原变量顺序）
    nullspace_basis: list[list[Fraction]]      # 零空间基（每个向量按原变量顺序）
    free_variables: list[int]                  # 自由变量的原始下标（0 基）
    pivot_variables: list[int]                 # 主元变量的原始下标（0 基）
    inconsistency_evidence: list[str]          # "0 = c" 证据行
    operations: list[Operation]                # 完整操作轨迹
    rref_augmented: list[list[Fraction]]       # 增广矩阵的 RREF（列交换后的坐标）
    column_permutation: list[int]              # 新列 -> 原列

    @property
    def unique(self) -> bool:
        return self.consistent and not self.free_variables


def solve(
    A: Sequence[Sequence],
    b: Sequence,
    *,
    column_pivoting: bool = False,
) -> Solution:
    """在域 Q 上精确求解 Ax = b。

    - 相容且欠定：给出特解与零空间基（解 = 特解 + 零空间任意线性组合）。
    - 不相容：给出 "0 = c (c != 0)" 证据行与秩判据 rank(A) < rank([A|b])。
    - 列交换（column_pivoting=True 时）记录置换，结果还原为原变量顺序。
    """
    a = to_matrix(A)
    rows = len(a)
    n = len(a[0]) if rows else 0
    if len(b) != rows:
        raise ValueError("b 的长度必须等于 A 的行数")
    bb = [to_fraction(v) for v in b]
    augmented = [a[i] + [bb[i]] for i in range(rows)]

    result = rref(
        augmented,
        column_pivoting=column_pivoting,
        column_swap_bound=n,  # 增广列不参与交换
    )
    m = result.matrix
    perm = result.column_permutation

    # 秩：主元列 < n 的属于 A；增广列上的主元意味着不相容
    rank_a = sum(1 for c in result.pivot_columns if c < n)
    rank_aug = result.rank

    # 不相容证据：形如 [0 ... 0 | c] (c != 0) 的行
    evidence: list[str] = []
    for i, row in enumerate(m):
        if all(v == 0 for v in row[:n]) and row[n] != 0:
            evidence.append(
                f"RREF 第 {i + 1} 行: 0 = {row[n]}（矛盾，0 != {row[n]}）"
            )
    consistent = not evidence
    if not consistent:
        evidence.append(
            f"秩判据: rank(A) = {rank_a} < rank([A|b]) = {rank_aug}，方程组不相容"
        )

    pivot_cols = [c for c in result.pivot_columns if c < n]
    free_cols = [c for c in range(n) if c not in set(pivot_cols)]

    particular: list[Fraction] | None = None
    nullspace_basis: list[list[Fraction]] = []
    if consistent:
        # 在新（列交换后）坐标下求解，再经置换还原到原变量顺序：
        # x_原[perm[k]] = x_新[k]
        particular_new = [Fraction(0) for _ in range(n)]
        for row_i, col in enumerate(pivot_cols):
            particular_new[col] = m[row_i][n]
        particular = [Fraction(0) for _ in range(n)]
        for new_col in range(n):
            particular[perm[new_col]] = particular_new[new_col]

        for free_col in free_cols:
            basis_new = [Fraction(0) for _ in range(n)]
            basis_new[free_col] = Fraction(1)
            for row_i, col in enumerate(pivot_cols):
                basis_new[col] = -m[row_i][free_col]
            basis_orig = [Fraction(0) for _ in range(n)]
            for new_col in range(n):
                basis_orig[perm[new_col]] = basis_new[new_col]
            nullspace_basis.append(basis_orig)

    return Solution(
        consistent=consistent,
        rank_a=rank_a,
        rank_augmented=rank_aug,
        num_variables=n,
        particular=particular,
        nullspace_basis=nullspace_basis,
        free_variables=sorted(perm[c] for c in free_cols),
        pivot_variables=sorted(perm[c] for c in pivot_cols),
        inconsistency_evidence=evidence,
        operations=result.operations,
        rref_augmented=m,
        column_permutation=perm,
    )


# ---------------------------------------------------------------------------
# 展示辅助
# ---------------------------------------------------------------------------

def fmt(v: Fraction) -> str:
    return str(v)


def format_matrix(matrix: Sequence[Sequence]) -> str:
    rows = [[fmt(to_fraction(v)) for v in row] for row in matrix]
    if not rows:
        return "[]"
    width = max(len(cell) for row in rows for cell in row)
    return "\n".join(
        "[ " + "  ".join(cell.rjust(width) for cell in row) + " ]" for row in rows
    )


def format_vector(v: Sequence) -> str:
    return "(" + ", ".join(fmt(to_fraction(x)) for x in v) + ")"


def matvec(A: Sequence[Sequence], x: Sequence) -> list[Fraction]:
    """精确矩阵-向量乘，用于验证解。"""
    a = to_matrix(A)
    xx = [to_fraction(v) for v in x]
    return [sum(row[j] * xx[j] for j in range(len(xx))) for row in a]


def print_trace(operations: Sequence[Operation]) -> None:
    print(f"操作轨迹（共 {len(operations)} 步，全程 {DOMAIN_FIELD}）:")
    for k, op in enumerate(operations, 1):
        print(f"  [{k:>2}] {op}")
