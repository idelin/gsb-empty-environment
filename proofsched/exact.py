"""Exact small-scale reference strategy.

Exhaustive DFS over subsets of derivation steps (respecting premise order),
returning the maximum achievable proved goal weight within the budget and one
witnessing plan.  Exponential in the number of rules: intended as a reference
oracle for small instances only.  Larger instances should use the heuristic
scheduler, which does not claim optimality.
"""
from __future__ import annotations

from dataclasses import dataclass

from .model import Fact, Goal, Rule


@dataclass(frozen=True)
class ExactResult:
    weight: float
    plan: tuple[str, ...]  # rule IDs in a feasible application order


def exact_max_weight(
    facts: dict[str, Fact],
    rules: dict[str, Rule],
    goals: dict[str, Goal],
    budget: int,
) -> ExactResult:
    base = frozenset(facts)
    goal_list = list(goals.values())
    total_weight = sum(g.weight for g in goal_list)

    def closure(applied: frozenset[str]) -> frozenset[str]:
        proved = set(base)
        growing = True
        while growing:
            growing = False
            for rid in applied:
                rule = rules[rid]
                if rule.conclusion not in proved and all(
                    p in proved for p in rule.premises
                ):
                    proved.add(rule.conclusion)
                    growing = True
        return frozenset(proved)

    def weight_of(proved: frozenset[str]) -> float:
        return sum(
            g.weight for g in goal_list if all(t in proved for t in g.targets)
        )

    best_weight = -1.0
    best_plan: tuple[str, ...] = ()
    seen: dict[frozenset[str], int] = {}

    def dfs(applied: frozenset[str], proved: frozenset[str], plan: list[str], remaining: int) -> None:
        nonlocal best_weight, best_plan
        current = weight_of(proved)
        if current > best_weight:
            best_weight = current
            best_plan = tuple(plan)
        if remaining == 0 or current >= total_weight:
            return
        previous = seen.get(applied)
        if previous is not None and previous >= remaining:
            return
        seen[applied] = remaining
        for rid in sorted(rules):
            if rid in applied:
                continue
            rule = rules[rid]
            if all(p in proved for p in rule.premises):
                dfs(
                    applied | {rid},
                    closure(applied | {rid}),
                    plan + [rid],
                    remaining - 1,
                )

    dfs(frozenset(), closure(frozenset()), [], budget)
    return ExactResult(weight=best_weight, plan=best_plan)
