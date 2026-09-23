"""有理矩阵高斯消元：求秩、解线性方程组、不相容证据。

设计约束：
  * 全部运算在有理数域 Q 上精确进行（fractions.Fraction），
    全程不转浮点，也不调用任何符号代数库（仅标准库）；
  * 消元过程记录完整的初等行/列变换轨迹，每一步都注明该变换的适用域；
  * 欠定系统给出特解与零空间基；不相容系统给出 "0 = 非零常数" 的证据行；
  * 列交换只作用于系数列并记录变量置换，最终结果一律按原始变量顺序还原。

运行环境：Python 3.11（仅标准库）。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Sequence

# ---------------------------------------------------------------------------
# 初等变换的适用域说明（每一步变换都附带，说明它在什么条件下保持等价）
# ---------------------------------------------------------------------------

DOMAIN_SWAP_ROWS = (
    "适用域：任意两行均可交换；相当于交换两个方程的书写次序，"
    "线性方程组的解集不变"
)
DOMAIN_SCALE_ROW = (
    "适用域：乘数 c 必须是非零有理数（c != 0）；方程两边同乘非零常数 "
    "得到的是等价方程，解集不变"
)
DOMAIN_ADD_MULTIPLE = (
    "适用域：倍数 c 可取任意有理数，且要求 i != j；把第 j 行的 c 倍加到 "
    "第 i 行，是行交换与行倍乘的复合，解集不变"
)
DOMAIN_SWAP_COLS = (
    "适用域：仅限系数列之间交换（不得涉及右端常数列），且必须同步交换对应 "
    "未知数的标签，方程组才保持等价；求解结束后将按原始变量顺序还原"
)


# ---------------------------------------------------------------------------
# 基础工具
# ---------------------------------------------------------------------------

def to_fraction(value) -> Fraction:
    """把 int / str / Fraction 精确转换为 Fraction。

    明确拒绝 float：本程序承诺不转浮点，浮点输入属于调用方错误。
    字符串接受 "3/2"、"-4" 这类 Fraction 支持的精确记法。
    """
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError(f"拒绝布尔输入 {value!r}：请用 0/1 整数")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value.strip())
    raise TypeError(f"拒绝非有理数输入 {value!r}：本程序不转浮点")


def fmt(num: Fraction) -> str:
    """把 Fraction 格式化为 'a/b' 或 'a'。"""
    if num.denominator == 1:
        return str(num.numerator)
    return f"{num.numerator}/{num.denominator}"


def matvec(A: Sequence[Sequence], x: Sequence) -> list[Fraction]:
    """精确计算 A @ x（用于验证解，全程有理数）。"""
    result = []
    for row in A:
        acc = Fraction(0)
        for a, v in zip(row, x):
            acc += to_fraction(a) * to_fraction(v)
        result.append(acc)
    return result


# ---------------------------------------------------------------------------
# 轨迹记录
# ---------------------------------------------------------------------------

@dataclass
class TraceEntry:
    """一步初等变换：操作名、具体内容、适用域说明。"""

    operation: str
    detail: str
    domain: str


# ---------------------------------------------------------------------------
# 核心消元（直接做到行最简形 RREF，便于读解）
# ---------------------------------------------------------------------------

def _eliminate(
    rows: list[list[Fraction]], num_coeff_cols: int, trace: list[TraceEntry]
) -> tuple[list[int], list[int]]:
    """对 rows 就地高斯消元（行前 num_coeff_cols 列为系数区）。

    返回 (pivot_cols, col_perm)：
      pivot_cols 为主元列（列交换后的当前列位置）；
      col_perm[j] 表示当前第 j 列对应原始未知数 x_{col_perm[j]+1}。
    """
    m = len(rows)
    width = len(rows[0]) if m else 0
    col_perm = list(range(num_coeff_cols))
    pivot_cols: list[int] = []
    r = 0
    c = 0
    while r < m and c < num_coeff_cols:
        pivot_row = next(
            (i for i in range(r, m) if rows[i][c] != 0), None
        )
        if pivot_row is None:
            # 当前列在主元行以下全为零：尝试列交换，把后面含非零元的列换上来
            swap_col = next(
                (
                    j
                    for j in range(c + 1, num_coeff_cols)
                    if any(rows[i][j] != 0 for i in range(r, m))
                ),
                None,
            )
            if swap_col is None:
                break  # 剩余行在系数区全为零，消元结束
            var_a, var_b = col_perm[c] + 1, col_perm[swap_col] + 1
            for row in rows:
                row[c], row[swap_col] = row[swap_col], row[c]
            col_perm[c], col_perm[swap_col] = col_perm[swap_col], col_perm[c]
            trace.append(
                TraceEntry(
                    "列交换",
                    f"C{c + 1} <-> C{swap_col + 1}"
                    f"（即未知数 x{var_a} <-> x{var_b}，最终答案将还原变量顺序）",
                    DOMAIN_SWAP_COLS,
                )
            )
            pivot_row = next(i for i in range(r, m) if rows[i][c] != 0)

        if pivot_row != r:
            rows[r], rows[pivot_row] = rows[pivot_row], rows[r]
            trace.append(
                TraceEntry(
                    "行交换",
                    f"R{r + 1} <-> R{pivot_row + 1}",
                    DOMAIN_SWAP_ROWS,
                )
            )

        pivot = rows[r][c]
        if pivot != 1:
            inv = Fraction(1, pivot)
            for j in range(width):
                rows[r][j] *= inv
            trace.append(
                TraceEntry(
                    "行倍乘",
                    f"R{r + 1} <- R{r + 1} * ({fmt(inv)})"
                    f"（即除以 {fmt(pivot)}，乘数非零，满足适用条件）",
                    DOMAIN_SCALE_ROW,
                )
            )

        for i in range(m):
            if i == r:
                continue
            factor = rows[i][c]
            if factor == 0:
                continue
            for j in range(width):
                rows[i][j] -= factor * rows[r][j]
            trace.append(
                TraceEntry(
                    "行倍加",
                    f"R{i + 1} <- R{i + 1} - ({fmt(factor)}) * R{r + 1}",
                    DOMAIN_ADD_MULTIPLE,
                )
            )

        pivot_cols.append(c)
        r += 1
        c += 1
    return pivot_cols, col_perm


# ---------------------------------------------------------------------------
# 对外接口
# ---------------------------------------------------------------------------

@dataclass
class EliminationResult:
    """rref() 的返回：行最简形、主元列、秩、变量置换、完整轨迹。"""

    rref: list[list[Fraction]]
    pivot_cols: list[int]
    rank: int
    col_perm: list[int]
    trace: list[TraceEntry] = field(default_factory=list)


def rref(A: Sequence[Sequence]) -> EliminationResult:
    """求矩阵 A 的行最简形与秩（精确有理数，记录轨迹）。"""
    rows = [[to_fraction(v) for v in row] for row in A]
    if not rows:
        return EliminationResult([], [], 0, [], [])
    width = len(rows[0])
    if any(len(row) != width for row in rows):
        raise ValueError("矩阵各行长度不一致")
    trace: list[TraceEntry] = []
    pivot_cols, col_perm = _eliminate(rows, width, trace)
    return EliminationResult(rows, pivot_cols, len(pivot_cols), col_perm, trace)


def rank(A: Sequence[Sequence]) -> int:
    """矩阵的秩（有理数域上精确计算）。"""
    return rref(A).rank


@dataclass
class Solution:
    """solve() 的返回。

    particular / nullspace_basis 一律按原始变量顺序给出
    （列交换造成的变量置换已在内部还原）。
    """

    consistent: bool
    rank: int
    num_vars: int
    particular: list[Fraction] | None
    nullspace_basis: list[list[Fraction]]
    evidence: list[list[Fraction]]  # 形如 [0,...,0 | c] (c != 0) 的增广行
    rref_aug: list[list[Fraction]]
    col_perm: list[int]
    trace: list[TraceEntry] = field(default_factory=list)


def solve(A: Sequence[Sequence], b: Sequence) -> Solution:
    """解线性方程组 Ax = b（有理数域上精确求解）。

    - 唯一解：particular 为该解，nullspace_basis 为空；
    - 欠定：particular 为特解（自由变量取 0），nullspace_basis 为零空间基，
      通解 = particular + sum(t_j * v_j)；
    - 不相容：consistent=False，evidence 给出 "0 = 非零常数" 的证据行。
    """
    rows_A = [[to_fraction(v) for v in row] for row in A]
    vec_b = [to_fraction(v) for v in b]
    if not rows_A:
        raise ValueError("系数矩阵不能为空")
    n = len(rows_A[0])
    if any(len(row) != n for row in rows_A):
        raise ValueError("系数矩阵各行长度不一致")
    if len(vec_b) != len(rows_A):
        raise ValueError("右端向量长度与矩阵行数不一致")

    aug = [rows_A[i] + [vec_b[i]] for i in range(len(rows_A))]
    trace: list[TraceEntry] = []
    pivot_cols, col_perm = _eliminate(aug, n, trace)
    r = len(pivot_cols)

    # ---- 不相容判定：系数区全零而右端非零的行，即 0 = c (c != 0) ----
    evidence = [
        row for row in aug if all(v == 0 for v in row[:n]) and row[n] != 0
    ]
    if evidence:
        return Solution(
            consistent=False,
            rank=r,
            num_vars=n,
            particular=None,
            nullspace_basis=[],
            evidence=evidence,
            rref_aug=aug,
            col_perm=col_perm,
            trace=trace,
        )

    # ---- 特解：自由变量取 0（先在“列交换后”的变量顺序下读出）----
    particular_perm = [Fraction(0)] * n
    for k, pc in enumerate(pivot_cols):
        particular_perm[pc] = aug[k][n]

    # ---- 零空间基：每个自由变量轮流取 1，其余自由变量取 0 ----
    pivot_set = set(pivot_cols)
    free_cols = [j for j in range(n) if j not in pivot_set]
    nullspace_perm: list[list[Fraction]] = []
    for f in free_cols:
        vec = [Fraction(0)] * n
        vec[f] = Fraction(1)
        for k, pc in enumerate(pivot_cols):
            vec[pc] = -aug[k][f]
        nullspace_perm.append(vec)

    # ---- 还原变量顺序：col_perm[j] = 当前第 j 列对应的原始变量下标 ----
    def restore(vec_perm: list[Fraction]) -> list[Fraction]:
        out = [Fraction(0)] * n
        for j in range(n):
            out[col_perm[j]] = vec_perm[j]
        return out

    return Solution(
        consistent=True,
        rank=r,
        num_vars=n,
        particular=restore(particular_perm),
        nullspace_basis=[restore(v) for v in nullspace_perm],
        evidence=[],
        rref_aug=aug,
        col_perm=col_perm,
        trace=trace,
    )


# ---------------------------------------------------------------------------
# 演示：python3.11 rational_gauss.py
# ---------------------------------------------------------------------------

def _fmt_vec(vec: Sequence[Fraction]) -> str:
    return "(" + ", ".join(fmt(v) for v in vec) + ")"


def _print_matrix(rows: Sequence[Sequence[Fraction]], indent: str = "    ") -> None:
    for row in rows:
        print(indent + "[ " + ", ".join(f"{fmt(v):>6}" for v in row) + " ]")


def _print_trace(trace: Sequence[TraceEntry]) -> None:
    for i, entry in enumerate(trace, 1):
        print(f"  [{i:2d}] {entry.operation}: {entry.detail}")
        print(f"       {entry.domain}")


def _report(title: str, A, b) -> None:
    print("=" * 72)
    print(title)
    print("=" * 72)
    sol = solve(A, b)
    n = sol.num_vars
    print(f"系数矩阵 A（{len(A)} x {n}），右端 b = {_fmt_vec([to_fraction(v) for v in b])}")
    print()
    print("消元轨迹（每步注明适用域）：")
    _print_trace(sol.trace)
    print()
    print(f"行最简形 [A|b]（列为当前变量顺序，置换 col_perm = "
          f"{[j + 1 for j in sol.col_perm]}）：")
    _print_matrix(sol.rref_aug)
    print()
    print(f"rank(A) = {sol.rank}，未知数个数 n = {n}")
    if not sol.consistent:
        for row in sol.evidence:
            lhs = " + ".join(f"{fmt(v)}*x{j + 1}" for j, v in enumerate(row[:n]))
            print(f"不相容证据：行 {_fmt_vec(row)} 对应方程 {lhs} = {fmt(row[n])}")
            print(f"即 0 = {fmt(row[n])}，矛盾 —— 方程组无解")
        return
    print(f"特解 x_p = {_fmt_vec(sol.particular)}（已按原始变量顺序还原）")
    if sol.nullspace_basis:
        print(f"零空间维数 = n - rank = {n - sol.rank}，零空间基：")
        for j, vec in enumerate(sol.nullspace_basis, 1):
            print(f"  v{j} = {_fmt_vec(vec)}")
        terms = " + ".join(f"t{j}*v{j}" for j in range(1, len(sol.nullspace_basis) + 1))
        print(f"通解 x = x_p + {terms}（t_j 为任意有理数）")
    else:
        print("零空间 = {0}，解唯一")
    # 精确验证
    check_p = matvec(A, sol.particular)
    print(f"验证 A*x_p = {_fmt_vec(check_p)}  （应等于 b）")
    for j, vec in enumerate(sol.nullspace_basis, 1):
        print(f"验证 A*v{j} = {_fmt_vec(matvec(A, vec))}  （应等于 0）")


def main() -> None:
    _report(
        "例 1：含分数系数的唯一解",
        [[Fraction(1, 2), 1], [1, -1]],
        [1, 1],
    )
    print()
    _report(
        "例 2：欠定系统（rank=2 < n=4），给特解与零空间基",
        [[1, 2, -1, 1], [2, 4, 1, 5], [3, 6, 0, 6]],
        [2, 7, 9],
    )
    print()
    _report(
        "例 3：不相容系统，给出 0 = 1 证据行",
        [[1, 1], [2, 2], [1, -1]],
        [1, 3, 0],
    )
    print()
    _report(
        "例 4：首列全零触发列交换，答案按原始变量顺序还原",
        [[0, 0, 1], [0, 1, 1], [0, 1, -1]],
        [1, 2, 0],
    )


if __name__ == "__main__":
    main()
