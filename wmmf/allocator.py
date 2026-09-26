"""加权最大最小公平（weighted max-min fair）注水分配器。

算法：所有未冻结需求的归一化速率（rate / weight）同步抬升，
直到某条链路饱和或某需求到达上限，冻结对应需求，进入下一轮。
全程使用 Fraction 精确运算，输出冻结顺序与瓶颈链路。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Demand, as_fraction


@dataclass(frozen=True)
class FreezeEvent:
    """一轮冻结事件。"""

    round_index: int                      # 轮次（从 0 开始）
    level: Fraction                       # 本轮冻结时的归一化水位
    demands: tuple[str, ...]              # 本轮冻结的需求
    bottleneck_links: tuple[str, ...]     # 导致本轮冻结的饱和链路
    capped: tuple[str, ...]               # 其中因到达自身上限而冻结的需求


@dataclass
class Allocation:
    """一次分配结果。"""

    rates: dict[str, Fraction]            # 需求 -> 速率
    link_loads: dict[str, Fraction]       # 链路 -> 负载
    link_caps: dict[str, Fraction]        # 链路 -> 容量
    demands: dict[str, Demand]
    events: list[FreezeEvent]

    def normalized_rate(self, name: str) -> Fraction:
        return self.rates[name] / self.demands[name].weight

    def is_satisfied(self, name: str) -> bool:
        cap = self.demands[name].cap
        return cap is not None and self.rates[name] >= cap

    def freeze_order(self) -> list[tuple[str, ...]]:
        return [event.demands for event in self.events]

    def bottleneck_links(self) -> list[str]:
        seen: list[str] = []
        for event in self.events:
            for link in event.bottleneck_links:
                if link not in seen:
                    seen.append(link)
        return seen

    def saturated_links(self) -> frozenset[str]:
        return frozenset(
            link for link, load in self.link_loads.items()
            if load == self.link_caps[link]
        )


def water_fill(link_caps, demands: dict[str, Demand]) -> Allocation:
    """对给定链路容量与需求集合做加权最大最小公平注水分配。"""
    link_caps = {str(name): as_fraction(cap) for name, cap in link_caps.items()}
    for name, demand in demands.items():
        if name != demand.name:
            raise ValueError(f"key {name!r} != demand.name {demand.name!r}")
        for link in demand.links:
            if link not in link_caps:
                raise ValueError(f"demand {name!r} uses unknown link {link!r}")

    rates = {name: Fraction(0) for name in demands}
    residual = dict(link_caps)
    active = set(demands)
    level = Fraction(0)
    events: list[FreezeEvent] = []
    round_index = 0

    while active:
        # 下一事件：链路饱和 或 需求到达上限，取最小归一化增量。
        delta = None
        for link in link_caps:
            wsum = sum(
                demands[d].weight for d in active if link in demands[d].links
            )
            if wsum > 0:
                cand = residual[link] / wsum
                if delta is None or cand < delta:
                    delta = cand
        for d in active:
            cap = demands[d].cap
            if cap is not None:
                cand = (cap - rates[d]) / demands[d].weight
                if delta is None or cand < delta:
                    delta = cand
        if delta is None:
            raise ValueError(
                "unbounded instance: some demand has neither cap nor link"
            )
        if delta < 0:  # 精确运算下不应发生
            raise ArithmeticError(f"negative water-filling step: {delta}")

        level += delta
        for d in active:
            rates[d] += delta * demands[d].weight
        for link in link_caps:
            wsum = sum(
                demands[d].weight for d in active if link in demands[d].links
            )
            residual[link] -= delta * wsum

        capped = sorted(
            d for d in active
            if demands[d].cap is not None and rates[d] == demands[d].cap
        )
        saturated = {
            link for link in link_caps
            if residual[link] == 0
            and any(link in demands[d].links for d in active)
        }
        blocked = sorted(
            d for d in active
            if d not in capped and any(link in saturated for link in demands[d].links)
        )
        frozen = sorted(set(capped) | set(blocked))
        if not frozen:
            raise ArithmeticError("water-filling made no progress")
        bottlenecks = sorted(
            link for link in saturated
            if any(link in demands[d].links for d in frozen)
        )
        events.append(FreezeEvent(
            round_index, level, tuple(frozen), tuple(bottlenecks), tuple(capped),
        ))
        active -= set(frozen)
        round_index += 1

    loads = {
        link: sum(rates[d] for d in demands if link in demands[d].links)
        for link in link_caps
    }
    return Allocation(rates, loads, link_caps, dict(demands), events)
