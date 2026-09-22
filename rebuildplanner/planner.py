"""最小重建成本规划。

语义（关键规则）：
1. 仅构建目标节点的传递依赖闭包；闭包外节点不出现在计划中。
2. 每个节点恰有一种处理方式：
   - reuse：产物有效（不在 failed 中）且无需被动重建，零成本。
   - restore：节点失效，但缓存摘要与节点 digest 一致，可恢复，成本 restore_cost。
   - rebuild：重建，成本 rebuild_cost。
3. 缓存摘要缺失或不一致 = 不可恢复。
4. 传播规则：重建一个节点会使其所有（闭包内的）下游产物失效，必须一并重建；
   恢复出的产物与原产物内容一致，不影响下游。
5. 成本按节点计一次：两个目标共享的中间产物即使被两条链路需要，也只计一次成本。

算法：闭包内按拓扑序（依赖在前）对每个可决策节点枚举
restore / rebuild，用集合记忆化求精确最优；规模较大时退化为按拓扑贪心的启发式。
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import Node, Problem

DEFAULT_RESTORE_COST = 0
# 决策节点（缓存可恢复的失效节点）超过此值时，改用具线性代价的启发式
EXACT_LIMIT = 20


class PlanError(ValueError):
    """无法生成或校验计划。"""


@dataclass
class PlannedNode:
    node_id: str
    action: str  # reuse | restore | rebuild
    cost: int
    reason: str
    digest: str


@dataclass
class Plan:
    actions: list[PlannedNode]
    total_cost: int
    targets: tuple[str, ...]
    closure: tuple[str, ...]


# ------------------------------------------------------------ 图处理


def target_closure(problem: Problem) -> tuple[str, ...]:
    """目标的传递依赖闭包（含目标自身），保持节点定义顺序（拓扑序）。"""
    required: set[str] = set()
    stack = list(problem.targets)
    while stack:
        nid = stack.pop()
        if nid in required:
            continue
        required.add(nid)
        stack.extend(dep.node_id for dep in problem.nodes[nid].deps)
    # model 已保证节点按依赖在前的顺序给出；只保留闭包成员
    return tuple(nid for nid in problem.nodes if nid in required)


def _dependents_within(problem: Problem, closure: tuple[str, ...]) -> dict[str, list[str]]:
    inside = set(closure)
    dependents: dict[str, list[str]] = {nid: [] for nid in closure}
    for nid in closure:
        for dep in problem.nodes[nid].deps:
            if dep.node_id in inside:
                dependents[dep.node_id].append(nid)
    return dependents


# ------------------------------------------------------------ 最优求解


@dataclass
class _Choice:
    node_id: str
    restore_cost: int
    rebuild_cost: int


def _solve(
    problem: Problem,
    closure: tuple[str, ...],
    dependents: dict[str, list[str]],
) -> dict[str, str]:
    """返回 node_id -> action（仅含 restore/rebuild；其余视为 reuse）。"""
    initially_failed = {nid for nid in closure if nid in problem.failed}
    # model 阶段已将摘要匹配的缓存成本写入 node.restore_cost；不匹配视为不可恢复
    restorable = {
        nid: (problem.nodes[nid].restore_cost if problem.nodes[nid].restore_cost is not None
              else DEFAULT_RESTORE_COST)
        for nid in initially_failed
        if problem.nodes[nid].restore_cost is not None
    }
    # 不可恢复的失效节点必须重建
    forced_must_rebuild = initially_failed - set(restorable)

    decisions = [
        _Choice(nid, restorable[nid], problem.nodes[nid].rebuild_cost)
        for nid in closure
        if nid in restorable
    ]

    def propagate_rebuilds(extra: set[str]) -> set[str]:
        """从必重建集合出发，传播到全部下游。"""
        rebuilt = set(forced_must_rebuild) | extra
        stack = list(forced_must_rebuild) + list(extra)
        while stack:
            current = stack.pop()
            for downstream in dependents[current]:
                if downstream not in rebuilt:
                    rebuilt.add(downstream)
                    stack.append(downstream)
        return rebuilt

    def evaluate(rebuild_decision_nodes: frozenset[str]) -> int:
        rebuilt = propagate_rebuilds(set(rebuild_decision_nodes))
        total = 0
        for choice in decisions:
            if choice.node_id in rebuilt:
                total += choice.rebuild_cost
            else:
                total += choice.restore_cost
        # 被动重建（本身未失效或不是决策点）的节点也要付重建成本
        decided = {c.node_id for c in decisions}
        for nid in rebuilt:
            if nid not in decided:
                total += problem.nodes[nid].rebuild_cost
        return total

    if len(decisions) > EXACT_LIMIT:
        rebuild_set = _greedy(problem, closure, decisions, dependents, forced_must_rebuild)
    else:
        rebuild_set = _exact(decisions, evaluate)

    rebuilt = propagate_rebuilds(rebuild_set)
    actions: dict[str, str] = {}
    for choice in decisions:
        actions[choice.node_id] = "rebuild" if choice.node_id in rebuilt else "restore"
    for nid in rebuilt:
        actions.setdefault(nid, "rebuild")
    return actions


def _exact(decisions: list[_Choice], evaluate) -> set[str]:
    """记忆化枚举：逐节点决定 restore(0)/rebuild(1)，取最小总成本。"""
    best: dict[frozenset, int] = {}
    best_choice: dict[frozenset, bool] = {}

    def dp(index: int, rebuild_set: frozenset[str]) -> int:
        if index == len(decisions):
            return evaluate(rebuild_set)
        key = (index, rebuild_set)
        if key in best:
            return best[key]
        nid = decisions[index].node_id
        restore_value = dp(index + 1, rebuild_set)
        rebuild_value = dp(index + 1, rebuild_set | {nid})
        # 成本相等时优先 restore（动作更少、更保守）
        if restore_value <= rebuild_value:
            best[key], best_choice[key] = restore_value, False
        else:
            best[key], best_choice[key] = rebuild_value, True
        return best[key]

    dp(0, frozenset())
    chosen: set[str] = set()
    state = frozenset()
    for index, choice in enumerate(decisions):
        if best_choice[(index, state)]:
            chosen.add(choice.node_id)
            state = state | {choice.node_id}
    return chosen


def _greedy(problem, closure, decisions, dependents, forced) -> set[str]:
    """按拓扑序贪心：若恢复成本 >= 重建引发的增量成本则重建。仅用于大规模输入。"""
    rebuilt = set(forced)

    def propagation_cost(start: str) -> int:
        total = 0
        stack = [start]
        seen = {start}
        while stack:
            current = stack.pop()
            for downstream in dependents[current]:
                if downstream not in rebuilt and downstream not in seen:
                    seen.add(downstream)
                    total += problem.nodes[downstream].rebuild_cost
                    stack.append(downstream)
        return total

    result: set[str] = set()
    for choice in decisions:
        # 重建该节点：自己的重建成本与传播增量；恢复：恢复成本
        downstream_cost = propagation_cost(choice.node_id)
        rebuild_delta = choice.rebuild_cost + downstream_cost - choice.restore_cost
        if rebuild_delta <= 0:
            result.add(choice.node_id)
            rebuilt.add(choice.node_id)
            stack = [choice.node_id]
            while stack:
                current = stack.pop()
                for downstream in dependents[current]:
                    if downstream not in rebuilt:
                        rebuilt.add(downstream)
                        stack.append(downstream)
    return result


# ------------------------------------------------------------ 计划生成


def build_plan(problem: Problem) -> Plan:
    closure = target_closure(problem)
    dependents = _dependents_within(problem, closure)
    actions = _solve(problem, closure, dependents)

    initially_failed = {nid for nid in closure if nid in problem.failed}
    forced_unrecoverable = {
        nid for nid in initially_failed
        if not (nid in problem.cache and problem.cache[nid][0] == problem.nodes[nid].digest)
    }

    planned: list[PlannedNode] = []
    total = 0
    for nid in closure:
        node = problem.nodes[nid]
        action = actions.get(nid, "reuse")
        if action == "restore":
            cost = node.restore_cost if node.restore_cost is not None else DEFAULT_RESTORE_COST
            reason = "产物失效；缓存摘要匹配，从缓存恢复"
        elif action == "rebuild":
            cost = node.rebuild_cost
            forced_by_dep = any(
                dep.node_id in actions and actions[dep.node_id] == "rebuild"
                for dep in node.deps
            )
            if forced_by_dep:
                reason = "依赖被重建，本节点产物失效，联动重建（缓存亦不可复用）"
            elif nid in forced_unrecoverable:
                reason = "产物失效且缓存缺失/摘要不匹配，不可恢复，必须重建"
            else:
                reason = "产物失效；重建的总成本低于恢复方案，选择重建"
        else:
            cost = 0
            reason = "产物有效且依赖未变化，直接复用"
        total += cost
        planned.append(PlannedNode(nid, action, cost, reason, node.digest))

    return Plan(
        actions=planned,
        total_cost=total,
        targets=problem.targets,
        closure=closure,
    )


# ------------------------------------------------------------ 逐节点校验


def verify_plan(problem: Problem, plan_payload: dict) -> list[str]:
    """对计划（dict 或 Plan.to_dict 结果）逐节点验证。返回错误列表，空列表=通过。"""
    errors: list[str] = []
    if not isinstance(plan_payload, dict):
        return ["计划必须是 JSON 对象"]
    raw_actions = plan_payload.get("actions")
    if not isinstance(raw_actions, list):
        return ["actions 必须是列表"]

    seen: set[str] = set()
    action_map: dict[str, dict] = {}
    for index, item in enumerate(raw_actions):
        if not isinstance(item, dict):
            errors.append(f"actions[{index}] 必须是对象")
            continue
        nid = item.get("node")
        if not isinstance(nid, str) or nid not in problem.nodes:
            errors.append(f"actions[{index}] 的 node 不存在：{nid!r}")
            continue
        if nid in seen:
            errors.append(f"节点在计划中重复出现：{nid}")
        seen.add(nid)
        action_map[nid] = item
        action = item.get("action")
        if action not in ("reuse", "restore", "rebuild"):
            errors.append(f"节点 {nid} 的 action 非法：{action!r}")

    targets = plan_payload.get("targets", list(problem.targets))
    if set(problem.targets) - set(targets):
        errors.append("计划未覆盖全部目标")

    closure = target_closure(problem)
    missing = [nid for nid in closure if nid not in seen]
    if missing:
        errors.append(f"计划缺少闭包内节点：{missing}")
    extra = [nid for nid in seen if nid not in closure]
    if extra:
        errors.append(f"计划包含闭包外节点：{extra}")

    # 拓扑序逐节点验证依赖满足
    for nid in closure:
        item = action_map.get(nid)
        if item is None:
            continue
        node = problem.nodes[nid]
        action = item.get("action")
        if item.get("digest") != node.digest:
            errors.append(f"节点 {nid} 的 digest 与输入不一致")
        dep_ok = True
        for dep in node.deps:
            dep_item = action_map.get(dep.node_id)
            if dep_item is None:
                dep_ok = False
                errors.append(f"节点 {nid} 缺少依赖 {dep.node_id}")
                continue
            if dep_item.get("action") == "rebuild":
                dep_ok = False
                if action != "rebuild":
                    errors.append(
                        f"节点 {nid} 的依赖 {dep.node_id} 被重建，本节点必须联动重建"
                    )
        if action == "restore":
            if nid not in problem.failed:
                errors.append(f"节点 {nid} 产物未失效，不应恢复（应 reuse）")
            cache_entry = problem.cache.get(nid)
            if cache_entry is None or cache_entry[0] != node.digest:
                errors.append(f"节点 {nid} 缓存缺失或摘要不匹配，不可恢复")
            elif item.get("cost") != cache_entry[1]:
                errors.append(f"节点 {nid} 的恢复成本应为 {cache_entry[1]}")
        if action == "rebuild":
            if item.get("cost") != node.rebuild_cost:
                errors.append(f"节点 {nid} 的重建成本应为 {node.rebuild_cost}")
        if action == "reuse":
            if nid in problem.failed:
                errors.append(f"节点 {nid} 已失效，不能 reuse")
            if dep_ok and item.get("cost", 0) != 0:
                errors.append(f"节点 {nid} reuse 成本必须为 0")

    declared_total = plan_payload.get("total_cost")
    computed = sum(
        (item.get("cost") or 0) for item in raw_actions if isinstance(item, dict)
    )
    if isinstance(declared_total, int) and declared_total != computed:
        errors.append(f"total_cost={declared_total} 与逐项成本之和 {computed} 不一致")

    # 最优性校验：与本求解器的最优成本对比
    expected = build_plan(problem).total_cost
    if computed > expected:
        errors.append(f"计划成本 {computed} 不是最小（最小为 {expected}）")
    return errors


# ------------------------------------------------------------ 序列化


def plan_to_dict(plan: Plan, problem: Problem) -> dict:
    return {
        "total_cost": plan.total_cost,
        "targets": list(plan.targets),
        "actions": [
            {
                "node": item.node_id,
                "package": problem.nodes[item.node_id].package,
                "version": str(problem.nodes[item.node_id].version),
                "action": item.action,
                "cost": item.cost,
                "digest": item.digest,
                "reason": item.reason,
                "deps": [d.node_id for d in problem.nodes[item.node_id].deps],
            }
            for item in plan.actions
        ],
    }


