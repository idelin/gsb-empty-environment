"""轨道与解恢复：验证每个等价类至少保留一个解，且被删解可恢复。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterable, Optional

from .csp import Problem
from .permutation import Permutation
from .solver import check_solution, solve_all


def _key(assignment: dict[str, Any]) -> tuple:
    return tuple(sorted(assignment.items()))


def orbit(group: Iterable[Permutation], assignment: dict[str, Any]) -> list[dict[str, Any]]:
    """assignment 在群作用下的轨道（去重）。"""
    seen: dict[tuple, dict[str, Any]] = {}
    for g in group:
        img = g.apply(assignment)
        seen.setdefault(_key(img), img)
    return list(seen.values())


def recover_solutions(
    group: Iterable[Permutation], retained: Iterable[dict[str, Any]]
) -> list[dict[str, Any]]:
    """恢复对称解的方法：对保留解施加群闭包中的全部置换并取并集。"""
    seen: dict[tuple, dict[str, Any]] = {}
    for sol in retained:
        for img in orbit(group, sol):
            seen.setdefault(_key(img), img)
    return list(seen.values())


def find_recovery(
    group: Iterable[Permutation],
    solution: dict[str, Any],
    retained: list[dict[str, Any]],
) -> Optional[tuple[Permutation, dict[str, Any]]]:
    """为（被删除的）解寻找恢复证人：g 与保留解 r 使 g·r == solution。"""
    target = _key(solution)
    retained_keys = {_key(r): r for r in retained}
    for g in group:
        for rk, r in retained_keys.items():
            if _key(g.apply(r)) == target:
                return g, r
    return None


@dataclass
class OrbitReport:
    """小规模问题的穷举验证报告。"""

    ok: bool
    total_solutions: int = 0
    retained: int = 0
    deleted: int = 0
    orbits: int = 0
    uncovered: list[dict[str, Any]] = field(default_factory=list)
    unrecoverable: list[dict[str, Any]] = field(default_factory=list)
    invalid_retained: list[dict[str, Any]] = field(default_factory=list)


def verify_orbit_coverage(problem: Problem, group: list[Permutation]) -> OrbitReport:
    """枚举全部解，验证：

    1. 保留解都通过独立检查器（确是原始问题的解）；
    2. 每个轨道至少保留一个解（不丢等价类）；
    3. 每个被删解都能由某个保留解经群元素恢复。
    """
    all_solutions = solve_all(problem, use_lex=False)
    retained = solve_all(problem, use_lex=True)
    retained_keys = {_key(r) for r in retained}

    invalid = [r for r in retained if not check_solution(problem, r)]

    # 轨道划分
    unseen = {_key(s): s for s in all_solutions}
    orbit_list: list[list[dict[str, Any]]] = []
    while unseen:
        _, seed = unseen.popitem()
        orb = orbit(group, seed)
        orbit_list.append(orb)
        for member in orb:
            unseen.pop(_key(member), None)

    uncovered: list[dict[str, Any]] = []
    for orb in orbit_list:
        if not any(_key(m) in retained_keys for m in orb):
            uncovered.append(orb[0])

    unrecoverable: list[dict[str, Any]] = []
    for sol in all_solutions:
        if _key(sol) not in retained_keys:
            if find_recovery(group, sol, retained) is None:
                unrecoverable.append(sol)

    ok = not invalid and not uncovered and not unrecoverable
    return OrbitReport(
        ok=ok,
        total_solutions=len(all_solutions),
        retained=len(retained),
        deleted=len(all_solutions) - len(retained),
        orbits=len(orbit_list),
        uncovered=uncovered,
        unrecoverable=unrecoverable,
        invalid_retained=invalid,
    )
