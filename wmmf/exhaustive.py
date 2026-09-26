"""小有理网格上的穷举核验。

把每个需求的速率限制在步长为 step 的有理网格上，枚举所有网格点，
检查是否存在可行网格点，其排序后的归一化速率向量在字典序上严格
优于给定分配（即给定分配不是加权最大最小公平）。

枚举点数受 budget 限制；超出预算时返回 UNDECIDED（预算未决），
与"可行""最优"结论严格分开，绝不混为一谈。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from itertools import product

from .model import Demand, as_fraction


class GridStatus(Enum):
    CONFIRMED_OPTIMAL = "confirmed_optimal"   # 网格内确认最优
    REFUTED = "refuted"                       # 找到严格更优的可行网格点
    UNDECIDED = "budget_undecided"            # 预算耗尽，未决


@dataclass
class GridResult:
    status: GridStatus
    checked: int                              # 实际检查的网格点数
    counterexample: dict[str, Fraction] | None = None


def _sorted_norm_vector(rates: dict[str, Fraction],
                        demands: dict[str, Demand]) -> list[Fraction]:
    return sorted(rates[name] / demands[name].weight for name in rates)


def grid_verify(link_caps, demands: dict[str, Demand],
                rates: dict[str, Fraction], step, budget: int) -> GridResult:
    """在小有理网格上穷举核验给定分配是否加权最大最小公平。"""
    step = as_fraction(step)
    if step <= 0:
        raise ValueError("grid step must be positive")
    link_caps = {str(k): as_fraction(v) for k, v in link_caps.items()}
    names = sorted(demands)

    grids: list[list[Fraction]] = []
    for name in names:
        demand = demands[name]
        if demand.cap is not None:
            hi = demand.cap
        elif demand.links:
            hi = min(link_caps[link] for link in demand.links)
        else:
            raise ValueError(f"demand {name}: unbounded, cannot grid-verify")
        values: list[Fraction] = []
        v = Fraction(0)
        while v <= hi:
            values.append(v)
            v += step
        grids.append(values)

    target = _sorted_norm_vector(rates, demands)
    checked = 0
    for combo in product(*grids):
        checked += 1
        if checked > budget:
            return GridResult(GridStatus.UNDECIDED, checked - 1)
        candidate = dict(zip(names, combo))
        if any(
            sum(candidate[n] for n in names if link in demands[n].links)
            > cap
            for link, cap in link_caps.items()
        ):
            continue
        if _sorted_norm_vector(candidate, demands) > target:
            return GridResult(GridStatus.REFUTED, checked, candidate)
    return GridResult(GridStatus.CONFIRMED_OPTIMAL, checked)
