"""Finite brute-force solver used only to cross-check the real solver.

Every package reachable from the roots is assigned one of its *finite* known
versions (the index is a finite fixture); all assignments are enumerated,
each target-gated dependency is evaluated, and full assignments are filtered.
Cycles need no special treatment because every package is bound at once.
"""
from __future__ import annotations

import itertools

from .index import Dependency, PackageIndex
from .solver import Solution, _version_key
from .target import Condition
from .version import Version


def brute_solve(
    index: PackageIndex,
    roots: list[Dependency],
    context: dict[str, str] | None = None,
) -> Solution | None:
    context = context or {}
    reachable = _closure(index, roots, context)
    order = sorted(reachable)
    domains = [index.versions(name) for name in order]
    if any(not options for options in domains):
        return None
    best: Solution | None = None
    for assignment in itertools.product(*domains):
        chosen = dict(zip(order, assignment))
        if not all(
            root.requirement.evaluate(context).contains(chosen[root.name])
            for root in roots
        ):
            continue
        if _assignment_valid(index, chosen, context):
            candidate = Solution(chosen)
            if best is None or _rank(candidate) > _rank(best):
                best = candidate
    return best


def brute_all_solutions(index, roots, context=None) -> list[Solution]:
    context = context or {}
    reachable = _closure(index, roots, context)
    order = sorted(reachable)
    domains = [index.versions(name) for name in order]
    solutions = []
    if any(not options for options in domains):
        return solutions
    for assignment in itertools.product(*domains):
        chosen = dict(zip(order, assignment))
        if not all(
            root.requirement.evaluate(context).contains(chosen[root.name])
            for root in roots
        ):
            continue
        if _assignment_valid(index, chosen, context):
            solutions.append(Solution(chosen))
    return solutions


def _assignment_valid(index: PackageIndex, chosen: dict[str, Version], context) -> bool:
    for name, version in chosen.items():
        release = index.release(name, version)
        for dep in release.dependencies:
            if dep.when is not None and not Condition.parse(dep.when).matches(context):
                continue
            if dep.name not in chosen:
                return False
            if not dep.requirement.evaluate(context).contains(chosen[dep.name]):
                return False
    return True


def _closure(index: PackageIndex, roots: list[Dependency], context) -> set[str]:
    pending = {root.name for root in roots}
    seen: set[str] = set()
    while pending:
        name = pending.pop()
        if name in seen:
            continue
        seen.add(name)
        for release in index.releases.get(name, ()):
            for dep in release.dependencies:
                if dep.when is not None and not Condition.parse(dep.when).matches(context):
                    continue
                if dep.name not in seen:
                    pending.add(dep.name)
    return seen


def _rank(solution: Solution):
    return tuple(sorted((name, _version_key(v)) for name, v in solution.chosen.items()))
