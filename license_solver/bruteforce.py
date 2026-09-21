"""Exhaustive enumeration of small graphs, used to cross-check the solver.

This is deliberately independent of the solver's search: it materializes the
full Cartesian product of (version x OR-branch) options and filters it.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from .expression import or_nodes
from .solver import evaluate_selection


@dataclass
class BruteResult:
    optimal_cost: tuple | None
    feasible_count: int
    explored: int


def _closure(repo, roots):
    seen, stack = set(), [r for r in roots if r in repo.packages]
    while stack:
        name = stack.pop()
        if name in seen:
            continue
        seen.add(name)
        for pv in repo.packages[name]:
            for dep in pv.deps:
                if dep in repo.packages and dep not in seen:
                    stack.append(dep)
    return seen


def _needed(choices, build):
    needed, stack = set(), [n for n in build.roots if n in choices]
    while stack:
        name = stack.pop()
        if name in needed:
            continue
        needed.add(name)
        pv, _ = choices[name]
        for dep in pv.deps:
            if dep not in choices:
                return None
            stack.append(dep)
    return needed


def _deps_ok(choices, needed):
    for name in needed:
        pv, _ = choices[name]
        for dep, spec in pv.deps.items():
            if dep not in needed:
                return False
            if not spec.range.contains(choices[dep][0].version):
                return False
    return True


def brute_force(repo, build) -> BruteResult:
    if any(root not in repo.packages for root in build.roots):
        return BruteResult(None, 0, 0)
    names = sorted(_closure(repo, build.roots))
    domains = []
    for name in names:
        options = []
        for pv in repo.packages[name]:
            if name in build.roots and not build.roots[name].contains(pv.version):
                continue
            if any(dep not in repo.packages for dep in pv.deps):
                continue
            n_or = len(or_nodes(pv.expr))
            combos = list(product((0, 1), repeat=n_or)) if n_or else [()]
            options.extend((pv, combo) for combo in combos)
        domains.append(options)
    best_cost = None
    feasible = 0
    explored = 0
    for picks in product(*domains):
        explored += 1
        choices = dict(zip(names, picks))
        needed = _needed(choices, build)
        if needed is None or not _deps_ok(choices, needed):
            continue
        outcome = evaluate_selection(
            repo, build, {n: choices[n] for n in needed}
        )
        if outcome.conflicts:
            continue
        feasible += 1
        if best_cost is None or outcome.cost < best_cost:
            best_cost = outcome.cost
    return BruteResult(best_cost, feasible, explored)
