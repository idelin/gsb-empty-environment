"""Trace why changing locked versions changes the solver's choices."""
from __future__ import annotations

from dataclasses import dataclass, field

from .index import Dependency, PackageIndex
from .solver import Solver, Unsatisfiable
from .version import Version


@dataclass(frozen=True)
class ChangeCause:
    package: str
    old_version: Version | None
    new_version: Version | None
    reason: str
    decisive: tuple[str, ...] = ()


@dataclass(frozen=True)
class LockChangeReport:
    before: dict[str, Version]
    after: dict[str, Version]
    causes: tuple[ChangeCause, ...]
    unsatisfiable_after: Unsatisfiable | None = None


def trace_lock_change(
    index: PackageIndex,
    roots: list[Dependency],
    context: dict[str, str],
    old_locked: dict[str, Version],
    new_locked: dict[str, Version],
) -> LockChangeReport:
    before = Solver(index, context, old_locked).solve(roots)
    try:
        after = Solver(index, context, new_locked).solve(roots)
    except Unsatisfiable as exc:
        return LockChangeReport(
            before.chosen,
            {},
            _causes(before.chosen, {}, new_locked, exc),
            exc,
        )
    return LockChangeReport(
        before.chosen, after.chosen, _causes(before.chosen, after.chosen, new_locked, None)
    )


def _causes(before, after, new_locked, unsat: Unsatisfiable | None) -> tuple[ChangeCause, ...]:
    causes: list[ChangeCause] = []
    for name in sorted(set(before) | set(after) | set(new_locked)):
        old_version = before.get(name)
        new_version = after.get(name)
        if old_version == new_version and name not in new_locked:
            continue
        if name in new_locked:
            requested = new_locked[name]
            if new_version == requested:
                reason = "forced to new locked pin"
            elif new_version is None:
                reason = "new lock pin participates in unsatisfiable core"
            else:
                reason = "locked pin unavailable; moved to best satisfying version"
            causes.append(ChangeCause(name, old_version, new_version, reason, ("lock", str(requested))))
        elif unsat is not None and any(req.package == name for req in unsat.core.requirements):
            causes.append(
                ChangeCause(
                    name,
                    old_version,
                    new_version,
                    "member of the minimal unsatisfiable core",
                    tuple(part for req in unsat.core.requirements for part in req.origin),
                )
            )
        elif old_version != new_version:
            causes.append(
                ChangeCause(
                    name,
                    old_version,
                    new_version,
                    "moved to satisfy constraints introduced by re-locked neighbours",
                )
            )
    return tuple(causes)
