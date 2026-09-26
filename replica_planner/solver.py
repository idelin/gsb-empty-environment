"""分支定界求解器：机架隔离 + 节点容量 + 反亲和约束下最小化总传输成本。

不调用任何外部求解器。搜索带扩展预算（max_expansions），结果严格分为：
- OPTIMAL:           搜索完成，成本已证明最优；
- FEASIBLE:          预算耗尽但已持有可行解（最优性未证明）；
- BUDGET_UNDECIDED:  预算耗尽且尚无可行解，预算内可行性未决；
- INFEASIBLE:        已证明不可行，并附容量诊断。
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction

from .diagnostics import CapacityDiagnostics, build_diagnostics
from .model import Node, ObjectSpec, to_fraction, validate_placement


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    INFEASIBLE = "infeasible"


@dataclass(frozen=True)
class SolveResult:
    status: Status
    placement: tuple[str, ...] | None   # 目标放置（节点 id），INFEASIBLE/未决时为 None
    cost: Fraction | None               # incumbent 成本；OPTIMAL 时即最优成本
    lower_bound: Fraction | None        # 已证明的全局下界
    budget: Fraction | None
    within_budget: bool | None          # None 表示预算问题未决
    expansions: int
    diagnostics: CapacityDiagnostics | None
    message: str


class _SearchLimitExceeded(Exception):
    pass


def solve(
    nodes,
    spec: ObjectSpec,
    budget=None,
    max_expansions: int = 1_000_000,
) -> SolveResult:
    """为对象选择 replicas 个满足约束的节点，最小化总传输成本。

    budget:         可选的成本预算（有理数），用于判定 within_budget；
    max_expansions: 分支定界的扩展节点数预算，耗尽时按 FEASIBLE / BUDGET_UNDECIDED 返回。
    """
    nodes = tuple(nodes)
    nodes_by_id: dict[str, Node] = {}
    for node in nodes:
        if node.node_id in nodes_by_id:
            raise ValueError(f"节点 id 重复: {node.node_id}")
        nodes_by_id[node.node_id] = node

    errors = validate_placement(nodes_by_id, spec.current)
    if errors:
        raise ValueError("当前放置非法: " + "; ".join(errors))

    budget = to_fraction(budget) if budget is not None else None
    current = set(spec.current)
    target_size = spec.replicas

    def place_cost(node: Node) -> Fraction:
        # 已持有副本的节点保留成本为 0；新节点收取传输成本
        return Fraction(0) if node.node_id in current else node.transfer_cost

    # 每个机架内容量 >= 1 的节点可选；同机架不同节点不满足机架隔离，故每机架至多选一个
    racks: dict[str, list[Node]] = {}
    for node in nodes:
        if node.capacity >= 1:
            racks.setdefault(node.rack, []).append(node)
    for members in racks.values():
        members.sort(key=lambda n: (place_cost(n), n.node_id))
    rack_names = list(racks)  # 保持输入顺序，保证结果确定性

    if len(rack_names) < target_size:
        diagnostics = build_diagnostics(nodes, spec)
        return SolveResult(
            status=Status.INFEASIBLE,
            placement=None,
            cost=None,
            lower_bound=None,
            budget=budget,
            within_budget=False if budget is not None else None,
            expansions=0,
            diagnostics=diagnostics,
            message=(
                f"不可行: 需要 {target_size} 个不同机架，"
                f"仅 {len(rack_names)} 个机架有可用容量，缺口 {diagnostics.deficit}"
            ),
        )

    rack_min = [place_cost(racks[rack][0]) for rack in rack_names]
    # suffix_mins[i] = 机架 i 及其之后所有 rack_min 的升序列表，用于下界计算
    suffix_mins: list[list[Fraction]] = [[] for _ in range(len(rack_names) + 1)]
    for i in range(len(rack_names) - 1, -1, -1):
        suffix_mins[i] = sorted(suffix_mins[i + 1] + [rack_min[i]])

    incumbent_cost: Fraction | None = None
    incumbent_nodes: tuple[str, ...] | None = None
    open_lower_bound: Fraction | None = None
    expansions = 0

    def note_bound(bound: Fraction) -> None:
        nonlocal open_lower_bound
        if open_lower_bound is None or bound < open_lower_bound:
            open_lower_bound = bound

    def dfs(idx: int, chosen: int, cost: Fraction, chosen_nodes: tuple[str, ...]) -> None:
        nonlocal incumbent_cost, incumbent_nodes, expansions
        need = target_size - chosen
        if need == 0:
            if incumbent_cost is None or cost < incumbent_cost:
                incumbent_cost = cost
                incumbent_nodes = chosen_nodes
            return
        if len(rack_names) - idx < need:
            return  # 剩余机架不足以达到目标副本数
        bound = cost + sum(suffix_mins[idx][:need])
        if incumbent_cost is not None and bound >= incumbent_cost:
            note_bound(bound)
            return
        expansions += 1
        if expansions > max_expansions:
            note_bound(bound)
            raise _SearchLimitExceeded()
        rack = rack_names[idx]
        for node in racks[rack]:
            dfs(idx + 1, chosen + 1, cost + place_cost(node), chosen_nodes + (node.node_id,))
        dfs(idx + 1, chosen, cost, chosen_nodes)  # 跳过该机架

    completed = True
    try:
        dfs(0, 0, Fraction(0), ())
    except _SearchLimitExceeded:
        completed = False

    if completed and incumbent_cost is not None:
        status = Status.OPTIMAL
        cost = incumbent_cost
        lower_bound = incumbent_cost
        message = f"已证明最优: 总传输成本 {incumbent_cost}"
    elif completed:
        status = Status.INFEASIBLE
        cost = None
        lower_bound = None
        message = "不可行: 搜索完成但未找到满足约束的放置"
    elif incumbent_cost is not None:
        status = Status.FEASIBLE
        cost = incumbent_cost
        lower_bound = open_lower_bound
        message = (
            f"搜索预算耗尽（{expansions} 次扩展），已找到可行解成本 {incumbent_cost}，"
            f"最优性未证明（下界 {open_lower_bound}）"
        )
    else:
        status = Status.BUDGET_UNDECIDED
        cost = None
        lower_bound = open_lower_bound
        message = (
            f"搜索预算耗尽（{expansions} 次扩展）且尚无可行解，"
            "预算内是否存在可行方案未决"
        )

    if budget is None:
        within_budget = None
    elif status is Status.INFEASIBLE:
        within_budget = False
    elif status is Status.OPTIMAL:
        within_budget = cost <= budget
    elif status is Status.FEASIBLE:
        within_budget = True if cost <= budget else None
    else:
        within_budget = None

    return SolveResult(
        status=status,
        placement=incumbent_nodes,
        cost=cost,
        lower_bound=lower_bound,
        budget=budget,
        within_budget=within_budget,
        expansions=expansions,
        diagnostics=build_diagnostics(nodes, spec) if status is Status.INFEASIBLE else None,
        message=message,
    )
