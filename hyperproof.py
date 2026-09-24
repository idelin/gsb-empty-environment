"""超图证明系统。

语义要点:
- 超边是 AND 语义: 一条超边的所有前置节点都成立, 结论才成立。
  绝不能把超边误当作 "任一前置成立即可" (OR)。
- 替代证明: 同一结论可由多条超边分别推出, 任一证明链成立则结论成立。
- 撤回: 外部种子 (seed) 可按时间区间断言与撤回, 派生结论随之重算。
- 证明成本: 节点成本 = 所有证明方案中 "超边成本 + 各前置最小成本之和" 的最小值;
  种子成本为 0。
- 循环证明必须有外部种子: 推导取最小不动点, 无种子的循环 (如 C->E->C) 不成立。
- 顶点与超边均使用字符串 ID; 保留孤立顶点; 允许平行 (多重) 超边。
- 时间区间左闭右开: [start, end)。
"""

from __future__ import annotations

from dataclasses import dataclass
from math import inf
from typing import Iterator


@dataclass(frozen=True)
class Interval:
    """左闭右开时间区间 [start, end)。"""

    start: float
    end: float

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"区间必须满足 start < end, 得到 [{self.start}, {self.end})")

    def contains(self, t: float) -> bool:
        return self.start <= t < self.end


@dataclass(frozen=True)
class Hyperedge:
    """超边: premises (全部成立) -> conclusion, 附带证明成本。"""

    id: str
    premises: tuple[str, ...]
    conclusion: str
    cost: float = 1.0

    def __post_init__(self) -> None:
        if self.cost < 0:
            raise ValueError("超边成本不能为负")


@dataclass(frozen=True)
class ProofNode:
    """一棵证明树的节点: vertex 由 seed 或某条超边推出。"""

    vertex: str
    via_edge: str | None  # None 表示外部种子
    premises: tuple["ProofNode", ...] = ()
    cost: float = 0.0


class HyperProofGraph:
    """带时间区间种子、撤回与证明成本的超图证明系统。"""

    def __init__(self) -> None:
        self._vertices: dict[str, None] = {}  # 有序集合, 保留孤立点
        self._edges: dict[str, Hyperedge] = {}  # 按边 ID 索引, 允许多重边
        self._seeds: dict[str, list[Interval]] = {}  # 外部种子及其有效区间

    # ------------------------------------------------------------------ #
    # 结构
    # ------------------------------------------------------------------ #
    def add_vertex(self, vertex_id: str) -> None:
        self._require_str(vertex_id, "顶点 ID")
        self._vertices.setdefault(vertex_id)

    def add_edge(
        self,
        edge_id: str,
        premises: tuple[str, ...] | list[str],
        conclusion: str,
        cost: float = 1.0,
    ) -> None:
        """添加超边; 允许同一 (premises, conclusion) 的多重边, 但边 ID 必须唯一。"""
        self._require_str(edge_id, "边 ID")
        self._require_str(conclusion, "结论顶点 ID")
        if edge_id in self._edges:
            raise ValueError(f"超边 ID 重复: {edge_id!r}")
        premises = tuple(premises)
        for p in premises:
            self._require_str(p, "前置顶点 ID")
        edge = Hyperedge(edge_id, premises, conclusion, float(cost))
        self._edges[edge_id] = edge
        for v in (*premises, conclusion):
            self._vertices.setdefault(v)

    @property
    def vertices(self) -> tuple[str, ...]:
        return tuple(self._vertices)

    @property
    def edges(self) -> tuple[Hyperedge, ...]:
        return tuple(self._edges.values())

    # ------------------------------------------------------------------ #
    # 种子: 断言与撤回
    # ------------------------------------------------------------------ #
    def assert_seed(self, vertex_id: str, start: float = 0.0, end: float = inf) -> None:
        """断言外部种子在区间 [start, end) 内成立。"""
        self._require_str(vertex_id, "顶点 ID")
        self._vertices.setdefault(vertex_id)
        self._seeds.setdefault(vertex_id, []).append(Interval(float(start), float(end)))

    def retract_seed(
        self, vertex_id: str, start: float | None = None, end: float | None = None
    ) -> None:
        """撤回种子: 指定 (start, end) 时仅移除精确匹配的区间, 否则移除该顶点全部种子。"""
        intervals = self._seeds.get(vertex_id)
        if not intervals:
            return
        if start is None and end is None:
            del self._seeds[vertex_id]
            return
        target = Interval(float(start), float(end))
        remaining = [iv for iv in intervals if iv != target]
        if remaining:
            self._seeds[vertex_id] = remaining
        else:
            del self._seeds[vertex_id]

    # ------------------------------------------------------------------ #
    # 推导
    # ------------------------------------------------------------------ #
    def _seeded_at(self, t: float) -> set[str]:
        return {v for v, ivs in self._seeds.items() if any(iv.contains(t) for iv in ivs)}

    def derived(self, t: float = 0.0) -> frozenset[str]:
        """时刻 t 成立的全部节点 (最小不动点; 无种子循环不成立)。"""
        derived = self._seeded_at(t)
        changed = True
        while changed:
            changed = False
            for edge in self._edges.values():
                if edge.conclusion not in derived and all(
                    p in derived for p in edge.premises
                ):
                    derived.add(edge.conclusion)
                    changed = True
        return frozenset(derived)

    def holds(self, vertex_id: str, t: float = 0.0) -> bool:
        return vertex_id in self.derived(t)

    # ------------------------------------------------------------------ #
    # 证明成本与证明树
    # ------------------------------------------------------------------ #
    def _min_costs(self, t: float) -> dict[str, float]:
        """Bellman-Ford 式松弛: 第 k 轮给出深度 <= k 的最优证明, |V| 轮收敛。"""
        cost: dict[str, float] = {v: 0.0 for v in self._seeded_at(t)}
        for _ in range(len(self._vertices)):
            changed = False
            for edge in self._edges.values():
                total = edge.cost
                for p in edge.premises:
                    pc = cost.get(p, inf)
                    if pc == inf:
                        total = inf
                        break
                    total += pc
                if total < cost.get(edge.conclusion, inf):
                    cost[edge.conclusion] = total
                    changed = True
            if not changed:
                break
        return cost

    def proof_cost(self, vertex_id: str, t: float = 0.0) -> float | None:
        """最小证明成本; 不可推导时返回 None。"""
        c = self._min_costs(t).get(vertex_id)
        return None if c is None or c == inf else c

    def proof(self, vertex_id: str, t: float = 0.0) -> ProofNode | None:
        """还原一棵最小成本证明树; 不可推导时返回 None。"""
        cost = self._min_costs(t)
        if cost.get(vertex_id, inf) == inf:
            return None
        seeds = self._seeded_at(t)
        best_edge: dict[str, Hyperedge] = {}
        for edge in self._edges.values():
            total = edge.cost + sum(cost.get(p, inf) for p in edge.premises)
            if total == cost.get(edge.conclusion, inf):
                best_edge[edge.conclusion] = edge

        def build(v: str, visiting: frozenset[str]) -> ProofNode:
            if v in seeds and cost[v] == 0.0:
                return ProofNode(vertex=v, via_edge=None, cost=0.0)
            edge = best_edge[v]
            children = tuple(
                build(p, visiting | {v}) for p in edge.premises if p not in visiting
            )
            return ProofNode(vertex=v, via_edge=edge.id, premises=children, cost=cost[v])

        return build(vertex_id, frozenset())

    # ------------------------------------------------------------------ #
    @staticmethod
    def _require_str(value: object, what: str) -> None:
        if not isinstance(value, str):
            raise TypeError(f"{what}必须是字符串, 得到 {type(value).__name__}")

    def __iter__(self) -> Iterator[Hyperedge]:
        return iter(self._edges.values())
