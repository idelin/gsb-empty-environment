"""二分图匹配：禁止边、必选边、偏好成本。

求解流程：
1. 预诊断必选边：禁止、不存在、共享左/右端点的冲突一次性报出，绝不静默丢弃。
2. 固定全部必选边，删除其占用的人和资源。
3. 在残图上跑“最小费用最大流”（逐次最短路 + Bellman-Ford），先保证基数
   最大，再在所有最大匹配中取成本最小者。
4. 所有运算使用 fractions.Fraction，整数/有理数成本均为精确结果。

不调用任何外部求解器，仅使用 Python 标准库。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Hashable, Iterable, Sequence

# ---------------------------------------------------------------------------
# 结果常量
# ---------------------------------------------------------------------------

STATUS_OPTIMAL = "optimal"                       # 已取得当前约束下的最优（最大基数 + 最小成本）

BUDGET_WITHIN = "within_budget"                 # 给定预算且最优成本不超过预算（可行）
BUDGET_EXCEEDED = "exceeds_budget"              # 给定预算但最优成本超过预算
BUDGET_UNDECIDED = "undecided"                  # 未给预算，预算可行性不判定

# 必选边诊断类型
D_SHARED_PERSON = "shared_person"               # 两条必选边共享同一个人（左侧冲突）
D_SHARED_RESOURCE = "shared_resource"           # 两条必选边共享同一个资源（右侧冲突）
D_FORBIDDEN = "forbidden"                       # 必选边同时被列为禁止边
D_UNKNOWN_EDGE = "unknown_edge"                 # 必选边不在允许边集合中
D_UNKNOWN_VERTEX = "unknown_vertex"             # 必选边引用了不存在的人或资源


# ---------------------------------------------------------------------------
# 必选边诊断
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class MandatoryDiagnostic:
    """单条必选边问题的诊断信息。"""

    kind: str
    message: str
    edges: tuple  # 涉及的 (person, resource) 对


class MandatoryConflictError(ValueError):
    """必选边存在冲突或非法时抛出，携带全部诊断（而不是只报第一条）。"""

    def __init__(self, diagnostics: Sequence[MandatoryDiagnostic]):
        self.diagnostics = list(diagnostics)
        summary = "; ".join(d.message for d in self.diagnostics)
        super().__init__(f"invalid mandatory edges: {summary}")


# ---------------------------------------------------------------------------
# 求解结果
# ---------------------------------------------------------------------------

@dataclass
class SolveResult:
    status: str                    # 目前恒为 STATUS_OPTIMAL（冲突时直接抛异常）
    matching: dict                 # person -> resource
    matched_count: int
    expected_count: int            # 满匹配规模 = min(|people|, |resources|)
    deficit: int                   # 缺额 = expected_count - matched_count（0 即满匹配）
    unmatched_people: list
    unmatched_resources: list
    total_cost: Fraction
    mandatory_matching: dict       # 被固定的必选边 person -> resource
    budget: Fraction | None
    budget_status: str

    @property
    def is_perfect(self) -> bool:
        return self.deficit == 0


# ---------------------------------------------------------------------------
# 最小费用最大流（容量均为 1；逐次最短路，Bellman-Ford 支持负费用，结果精确）
# ---------------------------------------------------------------------------

class _Edge:
    __slots__ = ("to", "rev", "cap", "cost")

    def __init__(self, to: int, rev: int, cap: int, cost: Fraction):
        self.to = to
        self.rev = rev
        self.cap = cap
        self.cost = cost


def _min_cost_max_flow(n_nodes: int, arcs: list, source: int, sink: int):
    """arcs: (u, v, cost)，容量统一为 1。返回 (最大流, 该最大流下的最小费用)。"""
    graph: list[list[_Edge]] = [[] for _ in range(n_nodes)]
    for u, v, cost in arcs:
        graph[u].append(_Edge(v, len(graph[v]), 1, cost))
        graph[v].append(_Edge(u, len(graph[u]) - 1, 0, -cost))

    flow = 0
    total_cost = Fraction(0)
    n = n_nodes

    while True:
        dist: list[Fraction | None] = [None] * n
        prev_node = [-1] * n
        prev_edge = [-1] * n
        dist[source] = Fraction(0)

        # Bellman-Ford；费用含负边（残量边），但最优流残量网络中不存在负环。
        for _ in range(n - 1):
            updated = False
            for u in range(n):
                if dist[u] is None:
                    continue
                for ei, edge in enumerate(graph[u]):
                    if edge.cap <= 0:
                        continue
                    new_dist = dist[u] + edge.cost
                    if dist[edge.to] is None or new_dist < dist[edge.to]:
                        dist[edge.to] = new_dist
                        prev_node[edge.to] = u
                        prev_edge[edge.to] = ei
                        updated = True
            if not updated:
                break

        if dist[sink] is None:
            break  # 无增广路，流已最大

        v = sink
        while v != source:
            u = prev_node[v]
            edge = graph[u][prev_edge[v]]
            edge.cap -= 1
            graph[v][edge.rev].cap += 1
            v = u
        flow += 1
        total_cost += dist[sink]

    return flow, total_cost, graph


# ---------------------------------------------------------------------------
# 对外入口
# ---------------------------------------------------------------------------

def solve(
    people: Iterable[Hashable],
    resources: Iterable[Hashable],
    edges: Iterable[tuple[Hashable, Hashable, object]],
    mandatory: Iterable[tuple[Hashable, Hashable]] = (),
    forbidden: Iterable[tuple[Hashable, Hashable]] = (),
    budget: object = None,
) -> SolveResult:
    """求最大基数、再最小成本的二分图匹配。

    参数
    ----
    people, resources: 左/右两侧顶点（任意可哈希标签，保持传入顺序）。
    edges: (person, resource, cost)；cost 为 int、Fraction 或可被 Fraction
        解析的有理数字符串（如 "1/3"）。同一对边取最小费用。
    mandatory: 必须入选的 (person, resource)；互相冲突时抛 MandatoryConflictError。
    forbidden: 禁止使用的 (person, resource)。
    budget: 预算，None 表示不判定（budget_status = undecided）。
    """
    people = list(dict.fromkeys(people))
    resources = list(dict.fromkeys(resources))
    people_set = set(people)
    resources_set = set(resources)
    forbidden_set = set(forbidden)

    # 1) 规范化边：校验顶点、剔除禁止边、重复边取最小费用。
    cost_of: dict[tuple, Fraction] = {}
    for entry in edges:
        if len(entry) != 3:
            raise ValueError(f"edge must be (person, resource, cost): {entry!r}")
        person, resource, raw_cost = entry
        if person not in people_set:
            raise ValueError(f"edge references unknown person: {person!r}")
        if resource not in resources_set:
            raise ValueError(f"edge references unknown resource: {resource!r}")
        if (person, resource) in forbidden_set:
            continue
        cost = Fraction(raw_cost)
        previous = cost_of.get((person, resource))
        if previous is None or cost < previous:
            cost_of[(person, resource)] = cost

    # 2) 预诊断必选边：收集全部问题，一次性报告。
    mandatory_pairs = [(p, r) for p, r in mandatory]
    diagnostics: list[MandatoryDiagnostic] = []
    seen_person: dict = {}
    seen_resource: dict = {}

    for pair in mandatory_pairs:
        person, resource = pair
        if person not in people_set or resource not in resources_set:
            diagnostics.append(
                MandatoryDiagnostic(
                    D_UNKNOWN_VERTEX,
                    f"mandatory edge {pair!r} references an unknown person or resource",
                    (pair,),
                )
            )
        elif pair in forbidden_set:
            diagnostics.append(
                MandatoryDiagnostic(
                    D_FORBIDDEN,
                    f"mandatory edge {pair!r} is also forbidden",
                    (pair,),
                )
            )
        elif pair not in cost_of:
            diagnostics.append(
                MandatoryDiagnostic(
                    D_UNKNOWN_EDGE,
                    f"mandatory edge {pair!r} is not among the allowed edges",
                    (pair,),
                )
            )

        if person in seen_person:
            other = seen_person[person]
            diagnostics.append(
                MandatoryDiagnostic(
                    D_SHARED_PERSON,
                    f"mandatory edges {other!r} and {pair!r} share person {person!r}; "
                    "a person cannot be matched twice",
                    (other, pair),
                )
            )
        else:
            seen_person[person] = pair

        if resource in seen_resource:
            other = seen_resource[resource]
            diagnostics.append(
                MandatoryDiagnostic(
                    D_SHARED_RESOURCE,
                    f"mandatory edges {other!r} and {pair!r} share resource {resource!r}; "
                    "a resource cannot be matched twice",
                    (other, pair),
                )
            )
        else:
            seen_resource[resource] = pair

    if diagnostics:
        raise MandatoryConflictError(diagnostics)

    # 3) 固定必选边，删除其占用的顶点。
    mandatory_matching: dict = {}
    mandatory_cost = Fraction(0)
    for person, resource in mandatory_pairs:
        mandatory_matching[person] = resource
        mandatory_cost += cost_of[(person, resource)]

    free_people = [p for p in people if p not in seen_person]
    free_resources = [r for r in resources if r not in seen_resource]
    p_index = {p: i for i, p in enumerate(free_people)}
    r_index = {r: i for i, r in enumerate(free_resources)}

    # 4) 建流网络：source -> person -> resource -> sink，容量 1。
    n_people = len(free_people)
    n_resources = len(free_resources)
    source = 0
    person_base = 1
    resource_base = person_base + n_people
    sink = resource_base + n_resources
    n_nodes = sink + 1

    arcs: list = []
    for i in range(n_people):
        arcs.append((source, person_base + i, Fraction(0)))
    for j in range(n_resources):
        arcs.append((resource_base + j, sink, Fraction(0)))

    free_edges = [
        (p, r) for (p, r) in cost_of
        if p in p_index and r in r_index
    ]
    for person, resource in free_edges:
        arcs.append(
            (person_base + p_index[person], resource_base + r_index[resource],
             cost_of[(person, resource)])
        )

    flow, flow_cost, residual = _min_cost_max_flow(n_nodes, arcs, source, sink)

    # 5) 从残量网络读出匹配（正向弧容量 1 -> 0 表示被使用）。
    matching: dict = dict(mandatory_matching)
    for i, person in enumerate(free_people):
        u = person_base + i
        for edge in residual[u]:
            if resource_base <= edge.to < sink and edge.cap == 0:
                matching[person] = free_resources[edge.to - resource_base]
                break

    matched_people = set(matching)
    matched_resources = set(matching.values())
    expected = min(len(people), len(resources))

    budget_value = None if budget is None else Fraction(budget)
    total_cost = mandatory_cost + flow_cost
    if budget_value is None:
        budget_status = BUDGET_UNDECIDED
    elif total_cost <= budget_value:
        budget_status = BUDGET_WITHIN
    else:
        budget_status = BUDGET_EXCEEDED

    return SolveResult(
        status=STATUS_OPTIMAL,
        matching=matching,
        matched_count=len(matching),
        expected_count=expected,
        deficit=expected - len(matching),
        unmatched_people=[p for p in people if p not in matched_people],
        unmatched_resources=[r for r in resources if r not in matched_resources],
        total_cost=total_cost,
        mandatory_matching=mandatory_matching,
        budget=budget_value,
        budget_status=budget_status,
    )
