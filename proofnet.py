"""基于超图的证明网络。

语义要点：
- 超边为 AND 语义：所有前置节点同时成立才产生结论（任一前置成立不够）。
- 同一结论可有多条超边（替代证明），任一超边全部前置成立即可维持结论。
- 支持撤回（种子与超边均可撤回），撤回后按剩余结构重新推导。
- 每条推导路径有成本：结论成本 = 超边成本 + 各前置最小成本之和，取最小值。
- 循环证明（如 C->E->C）在没有外部种子时不成立（最小不动点语义）。
- 顶点与边均使用字符串 ID；孤立顶点被保留；允许平行多重边。
- 时间区间为左闭右开 [start, end)。
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass

NEG_INF = float("-inf")
POS_INF = float("inf")

#: 默认区间：始终有效
ALWAYS: "Interval"


@dataclass(frozen=True)
class Interval:
    """左闭右开时间区间 [start, end)。"""

    start: float = NEG_INF
    end: float = POS_INF

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"区间须满足 start < end，得到 [{self.start}, {self.end})")

    def contains(self, t: float) -> bool:
        """t 落在 [start, end) 内：左闭右开。"""
        return self.start <= t < self.end


ALWAYS = Interval()


@dataclass(frozen=True)
class Seed:
    """外部种子：无需前置即可让某顶点在区间内成立。"""

    id: str
    vertex: str
    interval: Interval = ALWAYS
    cost: float = 0.0


@dataclass(frozen=True)
class Hyperedge:
    """超边：premises 全部成立时推出 conclusion。"""

    id: str
    premises: tuple[str, ...]
    conclusion: str
    cost: float = 1.0
    interval: Interval = ALWAYS


@dataclass(frozen=True)
class Proof:
    """一棵证明树：根为结论，source_id 指向所用种子或超边。"""

    vertex: str
    kind: str  # "seed" 或 "edge"
    source_id: str
    cost: float
    subproofs: tuple["Proof", ...] = ()

    def pretty(self, indent: str = "") -> str:
        head = f"{indent}{self.vertex}  <- {self.kind} '{self.source_id}' (cost={self.cost:g})"
        lines = [head]
        for sub in self.subproofs:
            lines.append(sub.pretty(indent + "    "))
        return "\n".join(lines)


class ProofNetwork:
    """超图证明网络：顶点、超边、种子与推导查询。"""

    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._edges: dict[str, Hyperedge] = {}
        self._seeds: dict[str, Seed] = {}

    # ------------------------------------------------------------------ 结构

    def add_vertex(self, vertex_id: str) -> None:
        """登记顶点；孤立顶点（无任何边关联）也会被保留。"""
        self._vertices.add(vertex_id)

    def add_edge(
        self,
        edge_id: str,
        premises,
        conclusion: str,
        cost: float = 1.0,
        interval: Interval = ALWAYS,
    ) -> None:
        """添加超边 premises -> conclusion（AND 语义）。

        允许平行多重边：相同前置与结论、不同 ID 的边可共存。
        前置中重复的顶点按一个计。成本须非负。
        """
        if edge_id in self._edges:
            raise ValueError(f"超边 ID 已存在: {edge_id!r}")
        if cost < 0:
            raise ValueError("超边成本须非负")
        unique_premises = tuple(dict.fromkeys(premises))
        if not unique_premises:
            raise ValueError("超边至少需要一个前置；零前置事实请用 add_seed")
        self._edges[edge_id] = Hyperedge(edge_id, unique_premises, conclusion, cost, interval)
        self._vertices.update(unique_premises)
        self._vertices.add(conclusion)

    def add_seed(
        self,
        seed_id: str,
        vertex: str,
        interval: Interval = ALWAYS,
        cost: float = 0.0,
    ) -> None:
        """添加外部种子：vertex 在 interval 内无需前置即成立。"""
        if seed_id in self._seeds:
            raise ValueError(f"种子 ID 已存在: {seed_id!r}")
        if cost < 0:
            raise ValueError("种子成本须非负")
        self._seeds[seed_id] = Seed(seed_id, vertex, interval, cost)
        self._vertices.add(vertex)

    def retract_edge(self, edge_id: str) -> None:
        """撤回超边。替代证明（其余推出同一结论的边）不受影响。"""
        del self._edges[edge_id]

    def retract_seed(self, seed_id: str) -> None:
        """撤回外部种子。"""
        del self._seeds[seed_id]

    @property
    def vertices(self) -> frozenset[str]:
        return frozenset(self._vertices)

    @property
    def edges(self) -> dict[str, Hyperedge]:
        return dict(self._edges)

    @property
    def seeds(self) -> dict[str, Seed]:
        return dict(self._seeds)

    # ------------------------------------------------------------------ 推导

    def _min_costs(self, t: float) -> dict[str, float]:
        """t 时刻各可证顶点的最小证明成本（最小不动点 + 类 Dijkstra 松弛）。

        从种子出发单调扩张，因此无种子的环（如 C->E->C）永远不可证。
        """
        costs: dict[str, float] = {}
        for seed in self._seeds.values():
            if seed.interval.contains(t):
                if seed.cost < costs.get(seed.vertex, POS_INF):
                    costs[seed.vertex] = seed.cost

        # 每个前置顶点 -> 以它为前置的（在 t 有效的）超边
        out_edges: dict[str, list[Hyperedge]] = {}
        for edge in self._edges.values():
            if edge.interval.contains(t):
                for premise in edge.premises:
                    out_edges.setdefault(premise, []).append(edge)

        heap = [(cost, vertex) for vertex, cost in costs.items()]
        heapq.heapify(heap)
        while heap:
            cost, vertex = heapq.heappop(heap)
            if cost > costs.get(vertex, POS_INF):
                continue  # 过期堆项
            for edge in out_edges.get(vertex, ()):
                if all(p in costs for p in edge.premises):
                    candidate = edge.cost + sum(costs[p] for p in edge.premises)
                    if candidate < costs.get(edge.conclusion, POS_INF):
                        costs[edge.conclusion] = candidate
                        heapq.heappush(heap, (candidate, edge.conclusion))
        return costs

    def holds(self, vertex: str, t: float = 0) -> bool:
        """vertex 在时刻 t 是否成立（可证）。"""
        return vertex in self._min_costs(t)

    def proof_cost(self, vertex: str, t: float = 0) -> float:
        """vertex 在时刻 t 的最小证明成本；不可证返回 inf。"""
        return self._min_costs(t).get(vertex, POS_INF)

    def derived(self, t: float = 0) -> frozenset[str]:
        """t 时刻所有成立的顶点。"""
        return frozenset(self._min_costs(t))

    def proof(self, vertex: str, t: float = 0) -> Proof | None:
        """返回 vertex 在时刻 t 的一棵最小成本证明树；不可证返回 None。"""
        costs = self._min_costs(t)
        if vertex not in costs:
            return None
        return self._build_proof(vertex, t, costs, frozenset())

    def _build_proof(
        self, vertex: str, t: float, costs: dict[str, float], visiting: frozenset[str]
    ) -> Proof | None:
        target = costs[vertex]
        for seed in self._seeds.values():
            if seed.vertex == vertex and seed.interval.contains(t) and seed.cost == target:
                return Proof(vertex, "seed", seed.id, target)
        for edge in self._edges.values():
            if edge.conclusion != vertex or not edge.interval.contains(t):
                continue
            if not all(p in costs for p in edge.premises):
                continue
            if edge.cost + sum(costs[p] for p in edge.premises) != target:
                continue
            if any(p in visiting for p in edge.premises):
                continue  # 零成本环可能产生等成本假证明，跳过
            subproofs = []
            for premise in edge.premises:
                sub = self._build_proof(premise, t, costs, visiting | {vertex})
                if sub is None:
                    break
                subproofs.append(sub)
            else:
                return Proof(vertex, "edge", edge.id, target, tuple(subproofs))
        return None
