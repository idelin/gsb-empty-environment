"""Backtracking resolver producing a consistent, newest-first version set."""

from __future__ import annotations

from dataclasses import dataclass

from .constraints import Constraint
from .registry import Registry
from .version import Version


@dataclass(frozen=True)
class SourcedConstraint:
    """A constraint plus where it came from, for reviewable conflicts."""

    constraint: Constraint
    source: str


class ResolutionConflict(Exception):
    """Raised when no version of a package satisfies its constraint set."""

    def __init__(self, package: str, constraints: list[SourcedConstraint]):
        self.package = package
        self.constraints = list(constraints)
        super().__init__(self.report())

    def report(self) -> str:
        lines = [
            f"conflict: no version of {self.package!r} satisfies "
            "all of these constraints:"
        ]
        for sourced in self.constraints:
            lines.append(f"  {sourced.constraint}  (required by {sourced.source})")
        return "\n".join(lines)


def resolve(
    registry: Registry, requirements: dict[str, Constraint]
) -> dict[str, Version]:
    """Resolve `requirements` against `registry`.

    Packages are considered in lexicographic name order; each takes the
    newest version consistent with every accumulated constraint, with
    backtracking when a later package invalidates an earlier choice.
    """
    constraints: dict[str, list[SourcedConstraint]] = {}
    selected: dict[str, Version] = {}

    def add_constraint(name: str, sourced: SourcedConstraint) -> None:
        constraints.setdefault(name, []).append(sourced)

    for name, constraint in sorted(requirements.items()):
        add_constraint(name, SourcedConstraint(constraint, "root"))

    def consistent(name: str) -> bool:
        if name not in selected:
            return True
        version = selected[name]
        return all(sc.constraint.satisfied_by(version) for sc in constraints[name])

    def candidates(name: str):
        sourced = constraints[name]
        allow_pre = any(sc.constraint.allows_prerelease() for sc in sourced)
        for release in registry.versions(name):
            version = release.version
            if version.is_prerelease and not allow_pre:
                continue
            if all(sc.constraint.satisfied_by(version) for sc in sourced):
                yield release

    def search() -> bool:
        unresolved = [name for name in constraints if name not in selected]
        if not unresolved:
            return True
        name = min(unresolved)
        options = list(candidates(name))
        if not options:
            raise ResolutionConflict(name, constraints[name])
        last_error: ResolutionConflict | None = None
        for release in options:
            added: list[str] = []
            selected[name] = release.version
            ok = True
            for dep_name, dep_constraint in release.dependencies:
                sourced = SourcedConstraint(
                    dep_constraint, f"{name}@{release.version}"
                )
                add_constraint(dep_name, sourced)
                added.append(dep_name)
                if not consistent(dep_name):
                    ok = False
                    last_error = ResolutionConflict(dep_name, constraints[dep_name])
                    break
            if ok:
                try:
                    if search():
                        return True
                except ResolutionConflict as error:
                    last_error = error
            for dep_name in added:
                constraints[dep_name].pop()
                if not constraints[dep_name]:
                    del constraints[dep_name]
            del selected[name]
        raise last_error or ResolutionConflict(name, constraints[name])

    if search():
        return dict(selected)
    raise AssertionError("unreachable: search() either returns True or raises")
