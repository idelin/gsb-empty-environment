"""二分图匹配：禁止边、必选边、偏好成本。

目标按字典序：先最大化匹配数，再最小化总成本。
仅使用 Python 3.11 标准库；不调用任何外部求解器。

状态语义（彼此分开）：
- OPTIMAL           : 达到满匹配（min(左, 右)）且成本最小。
- FEASIBLE          : 不存在满匹配，返回最大部分匹配（最小成本）与缺额。
- BUDGET_UNDECIDED  : 给出了预算，且最优成本超出预算；
                      预算内是否存在（可能更小的）可行匹配未作判定。
- INFEASIBLE        : 必选边冲突（共享端点 / 与禁止边冲突 / 端点未知），
                      在求解前预先诊断，绝不悄悄丢弃任何一条必选边。
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Dict, Hashable, Iterable, List, Optional, Tuple

Edge = Tuple[Hashable, Hashable]


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    INFEASIBLE = "infeasible"


@dataclass
class MatchingResult:
    status: Status
    matching: Dict[Hashable, Hashable] = field(default_factory=dict)
    cost: Fraction = Fraction(0)
    cardinality: int = 0
    full_size: int = 0
    deficit: int = 0
    unmatched_people: List[Hashable] = field(default_factory=list)
    unmatched_resources: List[Hashable] = field(default_factory=list)
    budget: Optional[Fraction] = None
    diagnostics: List[str] = field(default_factory=list)


def _to_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("成本/预算不接受布尔值")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的成本类型: {type(value)!r}")


def diagnose_mandatory(
    people: Iterable[Hashable],
    resources: Iterable[Hashable],
    mandatory: Iterable[Edge],
    forbidden: Iterable[Edge],
) -> List[str]:
    """预先诊断必选边冲突；返回诊断信息列表，空列表表示无冲突。"""
    people_set = set(people)
    resource_set = set(resources)
    forbidden_set = set(forbidden)
    diagnostics: List[str] = []

    by_person: Dict[Hashable, List[Hashable]] = {}
    by_resource: Dict[Hashable, List[Hashable]] = {}
    for person, resource in mandatory:
        if person not in people_set:
            diagnostics.append(f"必选边 ({person!r}, {resource!r}) 的左端点不在人员集合中")
        if resource not in resource_set:
            diagnostics.append(f"必选边 ({person!r}, {resource!r}) 的右端点不在资源集合中")
        if (person, resource) in forbidden_set:
            diagnostics.append(f"必选边 ({person!r}, {resource!r}) 同时是禁止边，直接冲突")
        by_person.setdefault(person, []).append(resource)
        by_resource.setdefault(resource, []).append(person)

    for person, rs in by_person.items():
        if len(rs) > 1:
            diagnostics.append(
                f"人员 {person!r} 被 {len(rs)} 条必选边占用（资源 {rs!r}），一人只能匹配一个资源"
            )
    for resource, ps in by_resource.items():
        if len(ps) > 1:
            diagnostics.append(
                f"资源 {resource!r} 被 {len(ps)} 条必选边占用（人员 {ps!r}），一个资源不能匹配两人"
            )
    return diagnostics


class _Arc:
    __slots__ = ("to", "rev", "cap", "cost")

    def __init__(self, to: int, rev: int, cap: int, cost: Fraction) -> None:
        self.to = to
        self.rev = rev
        self.cap = cap
        self.cost = cost


def _add_arc(graph: List[List[_Arc]], u: int, v: int, cap: int, cost: Fraction) -> int:
    graph[u].append(_Arc(v, len(graph[v]), cap, cost))
    graph[v].append(_Arc(u, len(graph[u]) - 1, 0, -cost))
    return len(graph[u]) - 1


def _min_cost_max_flow(graph: List[List[_Arc]], source: int, sink: int) -> Tuple[int, Fraction]:
    """逐条最短路增广（SPFA），单位容量下得到字典序 (最大流, 最小费用)。"""
    flow = 0
    cost = Fraction(0)
    n = len(graph)
    while True:
        dist: List[Optional[Fraction]] = [None] * n
        prev_v = [-1] * n
        prev_e = [-1] * n
        in_queue = [False] * n
        dist[source] = Fraction(0)
        queue: deque[int] = deque([source])
        in_queue[source] = True
        while queue:
            u = queue.popleft()
            in_queue[u] = False
            for i, arc in enumerate(graph[u]):
                if arc.cap <= 0:
                    continue
                cand = dist[u] + arc.cost  # type: ignore[operator]
                if dist[arc.to] is None or cand < dist[arc.to]:
                    dist[arc.to] = cand
                    prev_v[arc.to] = u
                    prev_e[arc.to] = i
                    if not in_queue[arc.to]:
                        queue.append(arc.to)
                        in_queue[arc.to] = True
        if dist[sink] is None:
            break
        v = sink
        while v != source:
            arc = graph[prev_v[v]][prev_e[v]]
            arc.cap -= 1
            graph[v][arc.rev].cap += 1
            v = prev_v[v]
        flow += 1
        cost += dist[sink]
    return flow, cost


def solve(
    people: Iterable[Hashable],
    resources: Iterable[Hashable],
    edges: Iterable[Tuple[Hashable, Hashable, object]],
    *,
    forbidden: Iterable[Edge] = (),
    mandatory: Iterable[Edge] = (),
    budget=None,
) -> MatchingResult:
    """求解带约束的二分图匹配。

    参数:
        people:    左部顶点（人员）。
        resources: 右部顶点（资源）。
        edges:     (人员, 资源, 成本) 三元组；成本为 int / Fraction / "p/q" 字符串。
        forbidden: 禁止边 (人员, 资源)。
        mandatory: 必选边 (人员, 资源)；必须全部出现在匹配中，冲突时整体报 INFEASIBLE。
        budget:    可选预算；最优成本超出预算时状态为 BUDGET_UNDECIDED。
    """
    people_list = list(dict.fromkeys(people))
    resource_list = list(dict.fromkeys(resources))
    people_set = set(people_list)
    resource_set = set(resource_list)
    forbidden_set = set(forbidden)
    mandatory_list = list(dict.fromkeys(mandatory))
    mandatory_set = set(mandatory_list)
    budget_frac = _to_fraction(budget) if budget is not None else None

    # 1) 必选边冲突预先诊断：任何冲突都整体报 INFEASIBLE，不悄悄丢边。
    diagnostics = diagnose_mandatory(people_set, resource_set, mandatory_list, forbidden_set)
    if diagnostics:
        return MatchingResult(
            status=Status.INFEASIBLE,
            full_size=min(len(people_list), len(resource_list)),
            budget=budget_frac,
            diagnostics=diagnostics,
        )

    # 2) 汇总允许边成本（重复边取最小成本）；必选边若未列出则按成本 0 补入。
    cost_map: Dict[Edge, Fraction] = {}
    for person, resource, cost in edges:
        if person not in people_set or resource not in resource_set:
            raise ValueError(f"边 ({person!r}, {resource!r}) 引用了未知顶点")
        key = (person, resource)
        value = _to_fraction(cost)
        if key not in cost_map or value < cost_map[key]:
            cost_map[key] = value
    for key in mandatory_list:
        cost_map.setdefault(key, Fraction(0))
    for key in forbidden_set:
        cost_map.pop(key, None)

    # 3) 固定必选边：其端点从残图中移除（必选边两两不冲突，可直接全部采纳）。
    forced_people = {p for p, _ in mandatory_list}
    forced_resources = {r for _, r in mandatory_list}
    forced_cost = sum(cost_map[key] for key in mandatory_list)
    free_people = [p for p in people_list if p not in forced_people]
    free_resources = [r for r in resource_list if r not in forced_resources]

    # 4) 建图：源 -> 人 -> 资源 -> 汇，单位容量，边上带成本。
    p_index = {p: i for i, p in enumerate(free_people)}
    r_index = {r: len(free_people) + i for i, r in enumerate(free_resources)}
    source = len(free_people) + len(free_resources)
    sink = source + 1
    graph: List[List[_Arc]] = [[] for _ in range(sink + 1)]
    for p in free_people:
        _add_arc(graph, source, p_index[p], 1, Fraction(0))
    for r in free_resources:
        _add_arc(graph, r_index[r], sink, 1, Fraction(0))
    edge_arcs: List[Tuple[Hashable, Hashable, int, int]] = []
    for (p, r), cost in cost_map.items():
        if p in forced_people or r in forced_resources:
            continue
        arc_idx = _add_arc(graph, p_index[p], r_index[r], 1, cost)
        edge_arcs.append((p, r, p_index[p], arc_idx))

    flow, flow_cost = _min_cost_max_flow(graph, source, sink)

    # 5) 提取匹配。
    matching: Dict[Hashable, Hashable] = dict(mandatory_list)
    for p, r, u, arc_idx in edge_arcs:
        if graph[u][arc_idx].cap == 0:  # 单位容量被用掉 => 该边在匹配中
            matching[p] = r

    cardinality = flow + len(mandatory_list)
    total_cost = flow_cost + forced_cost
    full_size = min(len(people_list), len(resource_list))
    deficit = full_size - cardinality

    # 6) 状态判定：预算未决与可行/最优分开。
    if budget_frac is not None and total_cost > budget_frac:
        status = Status.BUDGET_UNDECIDED
    elif deficit == 0:
        status = Status.OPTIMAL
    else:
        status = Status.FEASIBLE

    matched_people = set(matching)
    matched_resources = set(matching.values())
    return MatchingResult(
        status=status,
        matching=matching,
        cost=total_cost,
        cardinality=cardinality,
        full_size=full_size,
        deficit=deficit,
        unmatched_people=[p for p in people_list if p not in matched_people],
        unmatched_resources=[r for r in resource_list if r not in matched_resources],
        budget=budget_frac,
    )


def _demo() -> None:
    people = ["甲", "乙", "丙"]
    resources = ["岗位A", "岗位B", "岗位C"]
    edges = [
        ("甲", "岗位A", 5), ("甲", "岗位B", 2),
        ("乙", "岗位A", 3), ("乙", "岗位C", 4),
        ("丙", "岗位B", 1), ("丙", "岗位C", 6),
    ]
    res = solve(
        people, resources, edges,
        forbidden=[("丙", "岗位A")],
        mandatory=[("乙", "岗位C")],
        budget=20,
    )
    print("状态:", res.status.value)
    print("匹配:", res.matching)
    print("基数/满匹配/缺额:", res.cardinality, "/", res.full_size, "/", res.deficit)
    print("总成本:", res.cost, "预算:", res.budget)

    conflict = solve(
        people, resources, edges,
        mandatory=[("甲", "岗位A"), ("乙", "岗位A")],
    )
    print("冲突状态:", conflict.status.value)
    for line in conflict.diagnostics:
        print("  诊断:", line)


if __name__ == "__main__":
    _demo()
