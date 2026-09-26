"""数据模型：风险组、边、图，以及概率乘积的稳定（对数）表示。"""
from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, Iterable, List, Set, Tuple, Union

Number = Union[int, Fraction]


def to_number(value) -> Number:
    """把 int / float / str / Fraction 统一为精确数值（int 或 Fraction）。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool 不是合法数值")
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return Fraction(value).limit_denominator()
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的数值类型: {type(value)!r}")


@dataclass(frozen=True)
class RiskGroup:
    """共享风险组：同组边由同一失效事件控制，整条路径只计一次。"""

    name: str
    success: Fraction

    def __init__(self, name: str, success) -> None:
        prob = Fraction(to_number(success))
        if not Fraction(0) <= prob <= Fraction(1):
            raise ValueError(f"成功概率必须在 [0, 1] 内, 得到 {prob}")
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "success", prob)

    @property
    def nll(self) -> float:
        """负对数似然：概率乘积的稳定表示。

        边界：p = 0 -> +inf（必然失败，可直接剪枝）；p = 1 -> 0（中性元）。
        """
        if self.success == 0:
            return math.inf
        if self.success == 1:
            return 0.0
        return -math.log(float(self.success))


@dataclass(frozen=True)
class Edge:
    src: str
    dst: str
    cost: Number
    delay: Number
    group: str


class Graph:
    """有向图。每条边属于一个风险组；不同风险组的失效事件相互独立。"""

    def __init__(self) -> None:
        self.groups: Dict[str, RiskGroup] = {}
        self.edges: List[Edge] = []
        self.nodes: Set[str] = set()
        self._adj: Dict[str, List[int]] = {}

    def add_group(self, name: str, success) -> RiskGroup:
        if name in self.groups:
            raise ValueError(f"风险组重复: {name}")
        group = RiskGroup(name, success)
        self.groups[name] = group
        return group

    def add_edge(self, src: str, dst: str, cost, delay, group: str) -> int:
        if group not in self.groups:
            raise KeyError(f"未知风险组: {group}")
        edge = Edge(src, dst, to_number(cost), to_number(delay), group)
        self.edges.append(edge)
        self.nodes.update((src, dst))
        self._adj.setdefault(src, []).append(len(self.edges) - 1)
        self._adj.setdefault(dst, [])
        return len(self.edges) - 1

    def outgoing(self, node: str) -> List[Tuple[int, Edge]]:
        return [(i, self.edges[i]) for i in self._adj.get(node, [])]

    def success_of(self, groups: Iterable[str]) -> Fraction:
        """路径成功率 = 经过的不同风险组生存概率的乘积（精确有理数）。

        绝不把概率相加；同组边只贡献一次。
        """
        result = Fraction(1)
        for name in groups:
            result *= self.groups[name].success
        return result

    def nll_of(self, groups: Iterable[str]) -> float:
        """稳定的对数表示：sum(-log p_g)，排序后求和保证确定性。"""
        total = 0.0
        for name in sorted(groups):
            total += self.groups[name].nll
        return total
