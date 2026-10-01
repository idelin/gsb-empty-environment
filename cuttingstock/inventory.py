"""库存与跨批次联合优化。

Inventory 精确跟踪每种 (长度, 成本) 原料的剩余根数；批次顺序处理，
消耗与余料回库即时入账，因此同一根余料不可能被两个批次同时消费。
joint=True 时把所有批次需求合并为一次联合求解（规模需足够小）。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Demand, SolveResult, StockSpec, to_fraction
from .solver import Solver


class Inventory:
    """原料仓库：新料规格 + 余料，支持限量与不限量。"""

    def __init__(self, specs=()):
        self._entries = []  # [length, cost, count|None]
        for s in specs:
            s = s if isinstance(s, StockSpec) else StockSpec.of(*s)
            self.add(s.length, s.cost, s.count)

    def add(self, length, cost=0, count=None):
        length = to_fraction(length)
        cost = to_fraction(cost)
        if count is not None:
            count = int(count)
            if count < 0:
                raise ValueError("库存数量不能为负")
        for e in self._entries:
            if e[0] == length and e[1] == cost and (e[2] is None) == (count is None):
                if e[2] is not None:
                    e[2] += count
                return
        self._entries.append([length, cost, count])

    def specs(self):
        """当前可用规格（限量且已耗尽的条目不再出现）。"""
        return [
            StockSpec(e[0], e[1], e[2])
            for e in self._entries
            if e[2] is None or e[2] > 0
        ]

    def consume(self, length, cost=0, n=1):
        length = to_fraction(length)
        cost = to_fraction(cost)
        for e in self._entries:
            if e[0] == length and e[1] == cost:
                if e[2] is None:
                    return
                if e[2] < n:
                    raise ValueError(
                        f"库存不足: 长度 {length} 需要 {n} 根，仅剩 {e[2]} 根"
                    )
                e[2] -= n
                return
        raise ValueError(f"库存中没有长度 {length}、成本 {cost} 的原料")

    def __repr__(self):
        return f"Inventory({self.specs()!r})"


@dataclass
class BatchResult:
    name: str
    result: SolveResult


@dataclass
class CampaignResult:
    batches: list
    final_inventory: list

    @property
    def total_waste(self):
        vals = [b.result.waste for b in self.batches if b.result.waste is not None]
        return sum(vals, Fraction(0)) if vals else None

    @property
    def total_cost(self):
        vals = [b.result.cost for b in self.batches if b.result.cost is not None]
        return sum(vals, Fraction(0)) if vals else None


def _apply_result(inventory: Inventory, result: SolveResult):
    for bar in result.bars:
        inventory.consume(bar.stock_length, bar.cost)
    for r in result.reusable_remnants:
        inventory.add(r, 0, 1)


def run_batches(batches, inventory, kerf=0, min_remnant=0, max_nodes=100_000, joint=False):
    """跨批次优化。

    batches: [(名称, [Demand, ...]), ...]
    joint=False: 逐批次精确求解，上一批的可复用余料进入库存供后续批次使用；
    joint=True : 全部批次需求合并为一次联合求解。
    不可行的批次不消耗库存，后续批次继续用剩余库存求解。
    """
    results = []
    if joint:
        merged = {}
        order = []
        for _, demands in batches:
            for d in demands:
                d = d if isinstance(d, Demand) else Demand.of(*d)
                if d.length not in merged:
                    order.append(d.length)
                    merged[d.length] = 0
                merged[d.length] += d.quantity
        demands = [Demand(l, merged[l]) for l in order]
        res = Solver(demands, inventory.specs(), kerf, min_remnant, max_nodes).solve()
        if res.bars:
            _apply_result(inventory, res)
        results.append(BatchResult("<joint>", res))
    else:
        for name, demands in batches:
            res = Solver(demands, inventory.specs(), kerf, min_remnant, max_nodes).solve()
            if res.bars:
                _apply_result(inventory, res)
            results.append(BatchResult(name, res))
    return CampaignResult(results, inventory.specs())
