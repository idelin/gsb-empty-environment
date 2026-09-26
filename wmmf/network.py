"""支持增量再分配的网络对象：链路故障、容量调整、需求撤回。

关键语义：每当拓扑或需求集合变化导致饱和集合可能改变时，
**解冻所有原需求并重新注水**，而不是在旧分配上只继续扣剩余量。
这样原先被瓶颈链路冻结的需求在新拓扑下可以合法地获得更高速率。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .allocator import Allocation, water_fill
from .model import Demand, as_fraction


@dataclass
class ReallocReport:
    """一次再分配的增量报告。"""

    previous: Allocation | None
    current: Allocation
    increased: dict[str, tuple[Fraction, Fraction]]   # 需求 -> (旧, 新)
    decreased: dict[str, tuple[Fraction, Fraction]]
    unfrozen: tuple[str, ...]      # 曾被瓶颈冻结、本次获得更高速率的需求
    saturated_before: frozenset[str]
    saturated_after: frozenset[str]

    @property
    def saturation_set_changed(self) -> bool:
        return self.saturated_before != self.saturated_after


class FairNetwork:
    def __init__(self):
        self.link_caps: dict[str, Fraction] = {}
        self.demands: dict[str, Demand] = {}
        self.allocation: Allocation | None = None

    # ---- 拓扑与需求的变更 ----

    def add_link(self, name, capacity) -> None:
        self.link_caps[str(name)] = as_fraction(capacity)

    def set_link_capacity(self, name, capacity) -> None:
        name = str(name)
        if name not in self.link_caps:
            raise KeyError(f"unknown link {name!r}")
        self.link_caps[name] = as_fraction(capacity)

    def fail_link(self, name) -> None:
        """链路故障：容量降为 0。"""
        self.set_link_capacity(name, 0)

    def add_demand(self, demand: Demand) -> None:
        self.demands[demand.name] = demand

    def withdraw_demand(self, name) -> None:
        """需求撤回。"""
        del self.demands[str(name)]

    # ---- 分配 ----

    def allocate(self) -> Allocation:
        """全量重新注水（隐含解冻所有原需求）。"""
        self.allocation = water_fill(self.link_caps, self.demands)
        return self.allocation

    def reallocate(self) -> ReallocReport:
        """变更后的增量再分配，返回与上一次分配的差异报告。"""
        previous = self.allocation
        current = self.allocate()
        increased: dict[str, tuple[Fraction, Fraction]] = {}
        decreased: dict[str, tuple[Fraction, Fraction]] = {}
        unfrozen: list[str] = []
        if previous is not None:
            for name, rate in current.rates.items():
                old = previous.rates.get(name)
                if old is None:
                    continue
                if rate > old:
                    increased[name] = (old, rate)
                    # 旧分配中因瓶颈链路（而非上限）被冻结、现在提速者
                    if not previous.is_satisfied(name):
                        unfrozen.append(name)
                elif rate < old:
                    decreased[name] = (old, rate)
        return ReallocReport(
            previous=previous,
            current=current,
            increased=increased,
            decreased=decreased,
            unfrozen=tuple(sorted(unfrozen)),
            saturated_before=(
                previous.saturated_links() if previous else frozenset()
            ),
            saturated_after=current.saturated_links(),
        )
