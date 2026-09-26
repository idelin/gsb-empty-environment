"""基本数据模型：链路容量与带权重、上限、多链路路由的需求。

所有数值一律使用 fractions.Fraction 精确表示（合成整数/有理数），
不依赖任何求解器或第三方库。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def as_fraction(value) -> Fraction:
    """把 int / str / Fraction 统一转成 Fraction。"""
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


@dataclass(frozen=True)
class Demand:
    """一个带宽需求。

    - weight: 公平权重，归一化速率 = rate / weight。
    - cap:    需求上限（None 表示无上限）；到达上限后不再继续分配。
    - links:  该需求路由经过的链路（可多跳/多路径共享）。
              需求只获得**一个**标量速率，在其经过的每条链路上各占用
              该速率一次——绝不在每条链路上重复获配、重复计费。
    """

    name: str
    weight: Fraction
    cap: Fraction | None
    links: tuple[str, ...]

    def __init__(self, name, weight=Fraction(1), cap=None, links=()):
        weight = as_fraction(weight)
        if weight <= 0:
            raise ValueError(f"demand {name!r}: weight must be positive, got {weight}")
        cap = None if cap is None else as_fraction(cap)
        if cap is not None and cap < 0:
            raise ValueError(f"demand {name!r}: cap must be non-negative, got {cap}")
        object.__setattr__(self, "name", str(name))
        object.__setattr__(self, "weight", weight)
        object.__setattr__(self, "cap", cap)
        object.__setattr__(self, "links", tuple(str(link) for link in links))
