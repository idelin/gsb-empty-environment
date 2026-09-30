"""求解器：普通回溯枚举（不调用任何 SAT/SMT 库），以及独立检查器。

solve_all / solve_one 使用原始约束 + 已追加的字典序约束；
check_solution 是独立检查器，只针对原始约束与域，
凡是声称"有解"的地方都应通过它复核。
"""
from __future__ import annotations

from typing import Any, Optional

from .csp import Problem


def solve_all(
    problem: Problem, use_lex: bool = True, limit: Optional[int] = None
) -> list[dict[str, Any]]:
    """枚举全部解（可按 limit 截断）。use_lex=False 时求解原始问题。"""
    names = list(problem.names)
    domains = problem.domains
    constraints = problem.active_constraints(with_lex=use_lex)
    # 按变量索引分组约束：某变量定值后，scope 完全定值的约束才可检查
    position = {n: i for i, n in enumerate(names)}
    last_pos = []
    for c in constraints:
        last_pos.append(max(position[v] for v in c.scope))

    solutions: list[dict[str, Any]] = []
    assignment: dict[str, Any] = {}

    def consistent(upto: int) -> bool:
        for c, lp in zip(constraints, last_pos):
            if lp <= upto and not c.satisfied(assignment):
                return False
        return True

    def backtrack(i: int) -> None:
        if limit is not None and len(solutions) >= limit:
            return
        if i == len(names):
            solutions.append(dict(assignment))
            return
        var = names[i]
        for value in domains[var]:
            assignment[var] = value
            if consistent(i):
                backtrack(i + 1)
            del assignment[var]

    backtrack(0)
    return solutions


def solve_one(problem: Problem, use_lex: bool = True) -> Optional[dict[str, Any]]:
    """返回第一个解；无解返回 None。声称有解时调用方应用 check_solution 复核。"""
    found = solve_all(problem, use_lex=use_lex, limit=1)
    return found[0] if found else None


def check_solution(problem: Problem, assignment: dict[str, Any]) -> bool:
    """独立检查器：只验证原始问题的域与约束，不看任何对称破缺约束。

    与求解器不共享任何搜索/传播逻辑，直接逐条求值谓词。
    """
    if set(assignment.keys()) != set(problem.names):
        return False
    for v in problem.variables:
        if assignment[v.name] not in v.domain:
            return False
    return all(c.satisfied(assignment) for c in problem.constraints)
