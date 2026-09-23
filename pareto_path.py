"""预算约束下的双目标(成本/时延)帕累托最优简单路径求解。

模型约定:
- 有向多重图: 顶点与边均使用字符串 ID, 保留孤立点, 允许多重边(平行边)。
- 每条边带非负成本 cost、非负时延 delay, 以及可用时间区间 [start, end)(左闭右开)。
- 到达顶点后可等待: 沿边出发时刻为 max(到达时刻, start), 且必须满足 < end。
- 路径不重复顶点(简单路径)。由于权重非负且允许等待, 任意带环行走都可被
  一条简单路径弱支配, 因此按 (成本, 时延) 做支配剪枝即可, 前沿上的路径必为简单路径。
- 总成本不超过预算(含等于预算)即可行; 无可行路径时返回空集。
- 输出按成本升序的帕累托前沿; 被两维同时支配的路径不输出。
- max_results / max_labels 是明确的结果与计算上限; 触发上限时 truncated=True,
  此时 complete 为 False, 截断结果不得称为完整。
"""

from __future__ import annotations

import heapq
import math
from dataclasses import dataclass


@dataclass(frozen=True)
class Edge:
    """有向边。时间区间 [start, end) 左闭右开: start <= 出发时刻 < end。"""

    id: str
    source: str
    target: str
    cost: float
    delay: float
    start: float = 0.0
    end: float = math.inf

    def __post_init__(self) -> None:
        for name in ("id", "source", "target"):
            value = getattr(self, name)
            if not isinstance(value, str) or not value:
                raise ValueError(f"{name} 必须是非空字符串")
        if self.cost < 0:
            raise ValueError("cost 必须非负")
        if self.delay < 0:
            raise ValueError("delay 必须非负")
        if self.start > self.end:
            raise ValueError("时间区间 [start, end) 需满足 start <= end")


class Graph:
    """有向多重图: 保留孤立点与多重边。"""

    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._edges: list[Edge] = []
        self._adj: dict[str, list[Edge]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        if not isinstance(vertex_id, str) or not vertex_id:
            raise ValueError("顶点 ID 必须是非空字符串")
        self._vertices.add(vertex_id)
        self._adj.setdefault(vertex_id, [])

    def add_edge(self, edge: Edge) -> None:
        self.add_vertex(edge.source)
        self.add_vertex(edge.target)
        self._edges.append(edge)
        self._adj[edge.source].append(edge)

    @property
    def vertices(self) -> frozenset[str]:
        return frozenset(self._vertices)

    @property
    def edges(self) -> tuple[Edge, ...]:
        return tuple(self._edges)

    def outgoing(self, vertex_id: str) -> tuple[Edge, ...]:
        return tuple(self._adj.get(vertex_id, ()))


@dataclass(frozen=True)
class FrontierEntry:
    """一条帕累托最优路径: 总成本、总时延(含等待)、顶点序列、边 ID 序列。"""

    cost: float
    delay: float
    vertices: tuple[str, ...]
    edges: tuple[str, ...]


@dataclass(frozen=True)
class ParetoResult:
    source: str
    target: str
    budget: float
    max_results: int
    truncated: bool
    entries: tuple[FrontierEntry, ...]

    @property
    def complete(self) -> bool:
        """仅在未触发任何上限时为 True; 截断结果绝不能称为完整。"""
        return not self.truncated


def pareto_frontier(
    graph: Graph,
    source: str,
    target: str,
    budget: float,
    *,
    max_results: int = 1000,
    max_labels: int = 100_000,
    start_time: float = 0.0,
) -> ParetoResult:
    """计算 source -> target 在预算内的帕累托最优 (成本, 时延) 前沿。

    采用多目标标签设定算法(非负权重下按成本升序出堆, 支配即剪枝)。
    max_results 限制返回的前沿条数, max_labels 限制处理的标签总数;
    任一上限被触发时 truncated=True。
    """
    if source not in graph.vertices:
        raise KeyError(f"未知起点: {source!r}")
    if target not in graph.vertices:
        raise KeyError(f"未知终点: {target!r}")
    if max_results < 1:
        raise ValueError("max_results 必须 >= 1")
    if max_labels < 1:
        raise ValueError("max_labels 必须 >= 1")

    if budget < 0:  # 成本非负, 负预算必然无可行路
        return ParetoResult(source, target, budget, max_results, False, ())

    labels_at: dict[str, list[tuple[float, float]]] = {v: [] for v in graph.vertices}

    def is_dominated(vertex: str, cost: float, time: float) -> bool:
        return any(c <= cost and t <= time for c, t in labels_at[vertex])

    def add_label(vertex: str, cost: float, time: float) -> None:
        kept = [(c, t) for c, t in labels_at[vertex] if not (cost <= c and time <= t)]
        kept.append((cost, time))
        labels_at[vertex] = kept

    # 堆元素: (成本, 到达时刻, 序号, 顶点, 顶点路径, 边 ID 路径)
    heap: list[tuple[float, float, int, str, tuple[str, ...], tuple[str, ...]]] = [
        (0.0, float(start_time), 0, source, (source,), ())
    ]
    seq = 1
    found: list[FrontierEntry] = []
    processed = 0
    truncated = False

    while heap:
        cost, time, _, vertex, path_v, path_e = heapq.heappop(heap)
        if is_dominated(vertex, cost, time):
            continue
        add_label(vertex, cost, time)
        processed += 1
        if processed > max_labels:
            truncated = True
            break
        if vertex == target:
            found.append(
                FrontierEntry(
                    cost=cost,
                    delay=time - start_time,
                    vertices=path_v,
                    edges=path_e,
                )
            )
            continue
        for edge in graph.outgoing(vertex):
            # 不重复顶点由支配剪枝保证: 任何回到已访问顶点的扩展都会被
            # 该顶点上已有的(成本与时延均不更差的)标签支配, 不会入堆。
            depart = time if time > edge.start else edge.start
            if depart >= edge.end:  # 左闭右开: 出发时刻须满足 start <= t < end
                continue
            new_cost = cost + edge.cost
            if new_cost > budget:  # 预算边界等于时允许
                continue
            new_time = depart + edge.delay
            if is_dominated(edge.target, new_cost, new_time):
                continue
            heapq.heappush(
                heap,
                (
                    new_cost,
                    new_time,
                    seq,
                    edge.target,
                    path_v + (edge.target,),
                    path_e + (edge.id,),
                ),
            )
            seq += 1

    if len(found) > max_results:
        truncated = True
        found = found[:max_results]

    return ParetoResult(
        source=source,
        target=target,
        budget=budget,
        max_results=max_results,
        truncated=truncated,
        entries=tuple(found),
    )


def _build_demo_graph() -> Graph:
    g = Graph()
    g.add_vertex("iso")  # 孤立点: 保留在顶点集中
    g.add_edge(Edge("e1", "s", "a", cost=1, delay=4, start=0, end=10))
    g.add_edge(Edge("e2", "a", "t", cost=0, delay=5))
    g.add_edge(Edge("e3", "s", "b", cost=3, delay=1))
    g.add_edge(Edge("e4", "b", "t", cost=2, delay=1))
    g.add_edge(Edge("e5", "s", "t", cost=3, delay=5))
    g.add_edge(Edge("e6", "s", "t", cost=4, delay=6))  # 被 e5 在两维同时支配
    g.add_edge(Edge("e7", "s", "a", cost=1, delay=6))  # 与 e1 平行的多重边
    return g


def main() -> None:
    g = _build_demo_graph()
    result = pareto_frontier(g, "s", "t", budget=5, max_results=100)
    print(f"顶点(含孤立点): {sorted(g.vertices)}")
    print(f"边数(含多重边): {len(g.edges)}")
    print(
        f"预算={result.budget:g} 结果上限={result.max_results} "
        f"截断={result.truncated} 完整={result.complete}"
    )
    print("帕累托前沿(成本升序):")
    for entry in result.entries:
        path = "->".join(entry.vertices)
        print(
            f"  cost={entry.cost:g} delay={entry.delay:g} "
            f"路径={path} 边={list(entry.edges)}"
        )


if __name__ == "__main__":
    main()
