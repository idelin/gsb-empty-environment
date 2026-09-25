"""把分段仿射校正问题编译成有理系数线性约束系统。

变量顺序：alpha_0, beta_0, alpha_1, beta_1, ...（每段两个，均为自由变量）。

约束来源：

- 每个保留锚点：y_low <= alpha_i*x + beta_i <= y_high（i 为 x 所在段）；
- 每段斜率：rate_min_i <= alpha_i <= rate_max_i；
- 连续交界：alpha_i*b + beta_i = alpha_{i+1}*b + beta_{i+1}（显式等式）；
- 声明跳变交界：|跳变量| <= max_jump（若给出），
  即 -max_jump <= (a_{i+1}b+b_{i+1}) - (a_i b+b_i) <= max_jump。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Sequence

from .anchors import Anchor
from .model import PiecewiseModel


@dataclass
class System:
    """一个编译好的线性约束系统。"""

    n_vars: int
    free_vars: list[int]
    a_eq: list[list[Fraction]] = field(default_factory=list)
    b_eq: list[Fraction] = field(default_factory=list)
    a_ub: list[list[Fraction]] = field(default_factory=list)
    b_ub: list[Fraction] = field(default_factory=list)
    ub_tags: list[tuple[str, int]] = field(default_factory=list)
    anchor_rows: dict[int, tuple[int, int]] = field(default_factory=dict)


def _row(n: int, entries: dict[int, Fraction]) -> list[Fraction]:
    row = [Fraction(0)] * n
    for j, v in entries.items():
        row[j] = v
    return row


def build_system(model: PiecewiseModel, anchors: Sequence[Anchor]) -> System:
    """对给定模型与保留锚点集合编译约束系统。"""
    n_seg = model.n_segments
    n_vars = 2 * n_seg
    sys = System(n_vars=n_vars, free_vars=list(range(n_vars)))

    for i in range(n_seg):
        a = 2 * i
        sys.a_ub.append(_row(n_vars, {a: Fraction(1)}))
        sys.b_ub.append(model.rate_max[i])
        sys.ub_tags.append(("rate", i))
        sys.a_ub.append(_row(n_vars, {a: Fraction(-1)}))
        sys.b_ub.append(-model.rate_min[i])
        sys.ub_tags.append(("rate", i))

    for j, jn in enumerate(model.junctions):
        b = jn.breakpoint.seconds
        a_l, b_l = 2 * j, 2 * j + 1
        a_r, b_r = 2 * (j + 1), 2 * (j + 1) + 1
        coeffs = {a_l: b, b_l: Fraction(1), a_r: -b, b_r: Fraction(-1)}
        if jn.continuous:
            sys.a_eq.append(_row(n_vars, coeffs))
            sys.b_eq.append(Fraction(0))
        elif jn.max_jump is not None:
            jump = jn.max_jump.seconds
            sys.a_ub.append(_row(n_vars, coeffs))
            sys.b_ub.append(jump)
            sys.ub_tags.append(("jump", j))
            sys.a_ub.append(_row(n_vars, {k: -v for k, v in coeffs.items()}))
            sys.b_ub.append(jump)
            sys.ub_tags.append(("jump", j))

    for idx, anchor in enumerate(anchors):
        seg = model.segment_of(anchor.x)
        a, bcol = 2 * seg, 2 * seg + 1
        x = anchor.x
        row_hi = len(sys.a_ub)
        sys.a_ub.append(_row(n_vars, {a: x, bcol: Fraction(1)}))
        sys.b_ub.append(anchor.y_high)
        sys.ub_tags.append(("anchor", idx))
        row_lo = len(sys.a_ub)
        sys.a_ub.append(_row(n_vars, {a: -x, bcol: Fraction(-1)}))
        sys.b_ub.append(-anchor.y_low)
        sys.ub_tags.append(("anchor", idx))
        sys.anchor_rows[idx] = (row_hi, row_lo)

    return sys


def query_objective(model: PiecewiseModel, segment: int, x: Fraction) -> list[Fraction]:
    """查询点 x（位于 segment）处映射值的目标系数向量。"""
    obj = [Fraction(0)] * (2 * model.n_segments)
    obj[2 * segment] = x
    obj[2 * segment + 1] = Fraction(1)
    return obj
