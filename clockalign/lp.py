"""精确有理数线性规划：两阶段单纯形法（Bland 规则）。

仅使用标准库（``fractions.Fraction``），所有运算精确。

支持：

- 自由变量（自动拆分为 x = x+ - x-）；
- 等式 A_eq x = b_eq 与不等式 A x <= b（统一转换）；
- min / max 线性目标；
- 返回顶点解（用于给出"可行映射"的代表）；
- 对不可行系统提取约束索引构成的冲突核（贪心收缩成包含极小核）。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Callable, Optional, Sequence

ZERO = Fraction(0)
ONE = Fraction(1)


@dataclass
class LPResult:
    feasible: bool
    value: Optional[Fraction] = None
    x: Optional[list[Fraction]] = None
    unbounded: bool = False


def _pivot(table: list[list[Fraction]], r: int, c: int, ncols: int) -> None:
    pivot_val = table[r][c]
    table[r] = [v / pivot_val for v in table[r]]
    for i in range(len(table)):
        if i == r:
            continue
        factor = table[i][c]
        if factor:
            table[i] = [
                a - factor * b for a, b in zip(table[i], table[r], strict=True)
            ]


def _phase1_solve(
    table: list[list[Fraction]],
    nstruct: int,
    nart: int,
    basis: list[int],
) -> tuple[bool, list[int]]:
    """运行第一阶段（最小化人工变量之和）。

    table 的第 0 行须已是关于初始基 basis 的规范形。
    返回 (可行, 当前基变量列下标表)。
    """
    width = nstruct + nart + 1
    entering_cols = set(range(nstruct + nart))
    while True:
        c = next((j for j in entering_cols if table[0][j] < 0), None)
        if c is None:
            break
        pivot_r = None
        best_ratio = None
        for r in range(1, len(table)):
            a = table[r][c]
            if a > 0:
                ratio = table[r][-1] / a
                if best_ratio is None or ratio < best_ratio:
                    best_ratio = ratio
                    pivot_r = r
        if pivot_r is None:
            break  # 无界在第一阶段只会由数值构造错误引发
        basis[pivot_r - 1] = c
        _pivot(table, pivot_r, c, width)
    feasible = table[0][-1] == 0
    return feasible, basis


def _drop_artificial_columns(
    table: list[list[Fraction]], nstruct: int, nart: int, basis: list[int]
) -> tuple[int, list[int]]:
    """删除人工列前，先把仍留在基里的人工变量（退化，值为 0）换出。

    若某行在真实列上全零，则该约束冗余，整行删除。
    """
    width = nstruct + nart + 1
    drop_rows: list[int] = []
    for r in range(1, len(table)):
        if basis[r - 1] >= nstruct:
            c = next((j for j in range(nstruct) if table[r][j] != 0), None)
            if c is not None:
                basis[r - 1] = c
                _pivot(table, r, c, width)
            else:
                drop_rows.append(r)
    for r in reversed(drop_rows):
        del table[r]
        del basis[r - 1]
    for i in range(len(table)):
        table[i] = table[i][:nstruct] + [table[i][-1]]
    return nstruct + 1, basis


def solve_lp(
    objective: Sequence[Fraction],
    *,
    a_eq: Optional[Sequence[Sequence[Fraction]]] = None,
    b_eq: Optional[Sequence[Fraction]] = None,
    a_ub: Optional[Sequence[Sequence[Fraction]]] = None,
    b_ub: Optional[Sequence[Fraction]] = None,
    free_vars: Optional[Sequence[int]] = None,
    sense: str = "min",
) -> LPResult:
    """求解线性规划。

    变量顺序与 objective 一致；free_vars 中的变量无符号限制，
    其余变量隐式 >= 0。返回的 x 已把拆分变量合并回原变量。
    """
    n = len(objective)
    free = set(free_vars or ())

    # 自由变量拆分：原变量 j -> 列 2j+1 - 2j（若自由），否则列 j
    is_free = [j in free for j in range(n)]

    def expand_row(row: Sequence[Fraction]) -> list[Fraction]:
        out: list[Fraction] = []
        for j, a in enumerate(row):
            if is_free[j]:
                out.append(a)      # x_j^+
                out.append(-a)     # x_j^-
            else:
                out.append(a)
        return out

    ncols = 2 * len(free) + (n - len(free))

    eq_rows = [list(r) for r in (a_eq or ())]
    eq_rhs = [Fraction(b) for b in (b_eq or ())]
    ub_rows = [list(r) for r in (a_ub or ())]
    ub_rhs = [Fraction(b) for b in (b_ub or ())]

    # 逐行编译为标准形：
    #   等式  a x = b  (b<0 则整体取负)             -> +人工
    #   不等式 a x <= b (b>=0)                       -> +松弛 s
    #   不等式 a x <= b (b<0)  ->  -a x - s = -b     -> +剩余 s 与人工
    struct_rows: list[list[Fraction]] = []  # 实变量列系数
    slack_sign: list[int] = []              # 每行松弛/剩余列符号（0 表示无）
    rhs: list[Fraction] = []
    need_art: list[bool] = []

    for row, b in zip(eq_rows, eq_rhs, strict=True):
        if b < 0:
            row, b = [-a for a in row], -b
        struct_rows.append(row)
        slack_sign.append(0)
        rhs.append(b)
        need_art.append(True)
    for row, b in zip(ub_rows, ub_rhs, strict=True):
        if b >= 0:
            struct_rows.append(row)
            slack_sign.append(+1)
            rhs.append(b)
            need_art.append(False)
        else:
            struct_rows.append([-a for a in row])
            slack_sign.append(-1)
            rhs.append(-b)
            need_art.append(True)

    nrows = len(struct_rows)
    nslack = sum(1 for s in slack_sign if s != 0)
    nart = sum(1 for f in need_art if f)
    nstruct = ncols + nslack  # 实变量列 + 松弛/剩余列

    if nrows == 0:
        # 无约束：每个自由变量取 0；若非自由变量在目标中系数为负且无界
        x = [ZERO] * n
        if sense == "min":
            bad = any(j not in free and objective[j] < 0 for j in range(n))
        else:
            bad = any(j not in free and objective[j] > 0 for j in range(n))
        if bad:
            return LPResult(feasible=True, x=x, unbounded=True, value=None)
        val = sum(Fraction(objective[j]) * x[j] for j in range(n))
        return LPResult(feasible=True, x=x, value=val)

    width = nstruct + nart + 1
    table = [[ZERO] * width for _ in range(nrows + 1)]
    slack_col = ncols
    art_col = nstruct
    for i, (row, b) in enumerate(zip(struct_rows, rhs, strict=True)):
        exp = expand_row(row)
        for j, a in enumerate(exp):
            table[i + 1][j] = a
        if slack_sign[i] != 0:
            table[i + 1][slack_col] = Fraction(slack_sign[i])
            slack_col += 1
        if need_art[i]:
            table[i + 1][art_col] = ONE
            art_col += 1
        table[i + 1][-1] = b
    # 第一阶段目标行：min sum(art) 关于初始基的规范形
    for i in range(nrows):
        if need_art[i]:
            for j in range(nstruct):
                table[0][j] -= table[i + 1][j]
            table[0][-1] -= table[i + 1][-1]

    # 初始基：有人工列的行用人工列，否则用其松弛列
    basis: list[int] = []
    slack_col = ncols
    art_col = nstruct
    for i in range(nrows):
        s = slack_sign[i]
        sc = slack_col if s != 0 else -1
        if s != 0:
            slack_col += 1
        if need_art[i]:
            basis.append(art_col)
            art_col += 1
        else:
            basis.append(sc)

    feasible, basis = _phase1_solve(table, nstruct, nart, basis)
    if not feasible:
        return LPResult(feasible=False)

    width2, basis = _drop_artificial_columns(table, nstruct, nart, basis)

    # 第二阶段：装入真实目标（松弛列成本为 0）。basis 指向结构列。
    obj_exp: list[Fraction] = []
    for j, c in enumerate(objective):
        c = Fraction(c)
        if sense == "max":
            c = -c
        if is_free[j]:
            obj_exp.append(c)
            obj_exp.append(-c)
        else:
            obj_exp.append(c)
    obj_exp.extend([ZERO] * nslack)

    for j in range(nstruct):
        table[0][j] = obj_exp[j]
    table[0][-1] = ZERO
    for r, bc in enumerate(basis, start=1):
        factor = table[0][bc]
        if factor:
            table[0] = [a - factor * b for a, b in zip(table[0], table[r], strict=True)]

    while True:
        c = next((j for j in range(nstruct) if table[0][j] < 0), None)
        if c is None:
            break
        pivot_r = None
        best_ratio = None
        for r in range(1, len(table)):
            a = table[r][c]
            if a > 0:
                ratio = table[r][-1] / a
                if best_ratio is None or ratio < best_ratio:
                    best_ratio = ratio
                    pivot_r = r
        if pivot_r is None:
            return LPResult(feasible=True, unbounded=True)
        basis[pivot_r - 1] = c
        _pivot(table, pivot_r, c, width2)

    vals = [ZERO] * nstruct
    for r, bc in enumerate(basis, start=1):
        if bc < nstruct:
            vals[bc] = table[r][-1]
    x: list[Fraction] = []
    col = 0
    for j in range(n):
        if is_free[j]:
            x.append(vals[col] - vals[col + 1])
            col += 2
        else:
            x.append(vals[col])
            col += 1
    value = -table[0][-1] if sense == "min" else table[0][-1]
    return LPResult(feasible=True, value=value, x=x)


def feasibility(
    a_eq: Sequence[Sequence[Fraction]],
    b_eq: Sequence[Fraction],
    a_ub: Sequence[Sequence[Fraction]],
    b_ub: Sequence[Fraction],
    free_vars: Sequence[int],
    n_vars: int,
) -> LPResult:
    """只求可行性（目标全 0）。"""
    return solve_lp(
        [ZERO] * n_vars,
        a_eq=a_eq,
        b_eq=b_eq,
        a_ub=a_ub,
        b_ub=b_ub,
        free_vars=free_vars,
    )


def minimize_core(
    is_feasible: Callable[[frozenset[int]], bool],
    initial_core: Sequence[int],
) -> list[int]:
    """把一个不可行核贪心收缩为包含极小核。

    is_feasible(kept): 给定保留集合是否可行。核中的元素按序尝试删除，
    能删则删；一次遍历即得关于该初始核的包含极小不可行子集。
    """
    core = list(initial_core)
    for idx in list(core):
        trial = [c for c in core if c != idx]
        if not is_feasible(frozenset(trial)):
            core = trial
    return core
