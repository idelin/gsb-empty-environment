"""Backtracking multi-package dependency solver.

Per-package constraints are intersected while releases are chosen; choosing a
release activates its target-gated dependencies.  A package that is already
chosen is simply re-checked against every new constraint, so dependency
cycles are handled uniformly: compatible when every constraint on the cycle
is satisfied, conflicting otherwise.

At every pruning point a minimal core of requirements excluding all
candidate versions is recorded and surfaced via :class:`Unsatisfiable`.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .index import Dependency, PackageIndex, Release
from .target import Condition
from .version import Version
from .versionset import VersionSet


@dataclass(frozen=True)
class Requirement:
    package: str
    set: VersionSet
    origin: tuple[str, ...]
    dependency: Dependency | None = None


@dataclass(frozen=True)
class Solution:
    chosen: dict[str, Version]

    def as_dict(self) -> dict[str, str]:
        return {name: str(version) for name, version in sorted(self.chosen.items())}


@dataclass(frozen=True)
class UnsatCore:
    package: str
    requirements: tuple[Requirement, ...]
    available: tuple[Version, ...]


class Unsatisfiable(Exception):
    def __init__(self, core: UnsatCore):
        self.core = core
        super().__init__(self.describe(core))

    @staticmethod
    def describe(core: UnsatCore) -> str:
        chains = "; ".join(" -> ".join(req.origin) for req in core.requirements)
        sets_ = " & ".join(req.set.serialize() for req in core.requirements)
        available = ", ".join(str(v) for v in core.available) or "<none>"
        return (
            f"no candidate for {core.package}: {sets_}\n"
            f"constraint paths: {chains}\n"
            f"known versions: {available}"
        )


@dataclass
class Solver:
    index: PackageIndex
    context: dict[str, str] = field(default_factory=dict)
    prefer_locked: dict[str, Version] = field(default_factory=dict)

    def solve(self, roots: list[Dependency]) -> Solution:
        requirements: dict[str, list[Requirement]] = {}
        for root in roots:
            requirements.setdefault(root.name, []).append(
                Requirement(
                    root.name,
                    root.requirement.evaluate(self.context),
                    ("<root>",),
                    root,
                )
            )
        for name, version in self.prefer_locked.items():
            requirements.setdefault(name, []).append(
                Requirement(name, _point_set(version), ("<lock>",), None)
            )
        best_core: list[UnsatCore | None] = [None]
        result = self._search(requirements, {}, lambda core: best_core.__setitem__(
            0, _better_core(best_core[0], core)
        ))
        if result is None:
            raise Unsatisfiable(best_core[0]) if best_core[0] else RuntimeError("no core recorded")
        return result

    def _search(
        self,
        requirements: dict[str, list[Requirement]],
        chosen: dict[str, Version],
        record,
    ) -> Solution | None:
        package = self._pick_package(requirements, chosen)
        if package is None:
            return Solution(dict(chosen))

        reqs = requirements[package]
        if package in chosen:
            if all(req.set.contains(chosen[package]) for req in reqs):
                next_chosen = dict(chosen)
                return self._search(requirements, next_chosen, record)
            record(self._build_core(package, reqs))
            return None

        constraint = _intersect(reqs)
        candidates = [
            release
            for release in self.index.releases.get(package, ())
            if constraint.contains(release.version)
        ]
        if not candidates:
            record(self._build_core(package, reqs))
            return None

        for release in self._order_candidates(candidates):

            next_requirements = {name: list(items) for name, items in requirements.items()}
            next_chosen = dict(chosen)
            next_chosen[package] = release.version
            active = [
                dep
                for dep in release.dependencies
                if dep.when is None or Condition.parse(dep.when).matches(self.context)
            ]
            failure_core: UnsatCore | None = None
            for dep in active:
                origin = (*_last_origin(reqs, package, release.version), dep.name)
                requirement = Requirement(
                    dep.name,
                    dep.requirement.evaluate(self.context),
                    origin,
                    dep,
                )
                if dep.name in next_chosen and not requirement.set.contains(next_chosen[dep.name]):
                    failure_core = self._build_core(
                        dep.name, next_requirements.get(dep.name, []) + [requirement]
                    )
                    break
                next_requirements.setdefault(dep.name, []).append(requirement)
            if failure_core is not None:
                record(failure_core)
                continue
            result = self._search(next_requirements, next_chosen, record)
            if result is not None:
                return result
        return None

    def _pick_package(
        self,
        requirements: dict[str, list[Requirement]],
        chosen: dict[str, Version],
    ) -> str | None:
        pending = [name for name in requirements if name not in chosen]
        if not pending:
            return None

        def rank(name: str):
            constraint = _intersect(requirements[name])
            count = sum(
                1
                for release in self.index.releases.get(name, ())
                if constraint.contains(release.version)
            )
            connecting = 0 if name in self.prefer_locked else 1
            return (count, connecting, name)

        return min(pending, key=rank)

    def _order_candidates(self, candidates: list[Release]) -> list[Release]:
        locked = self.prefer_locked

        def key(release: Release):
            version = release.version
            return (
                0 if locked.get(release.package) == version else 1,
                -version.major,
                -version.minor,
                -version.patch,
                0 if version.channel is None else 1,
                version.channel or "",
                -(version.seq or 0),
            )

        return sorted(candidates, key=key)

    def _build_core(self, package: str, requirements: list[Requirement]) -> UnsatCore:
        minimal = _minimal_core(requirements, self.index.versions(package))
        return UnsatCore(package, tuple(minimal), tuple(self.index.versions(package)))


def _last_origin(reqs: list[Requirement], package: str, version: Version) -> tuple[str, ...]:
    for requirement in reversed(reqs):
        if requirement.dependency is not None:
            return (*requirement.origin, f"{package}@{version}")
    return ("<root>", f"{package}@{version}")


def _better_core(old: UnsatCore | None, new: UnsatCore) -> UnsatCore:
    if old is None or len(new.requirements) < len(old.requirements):
        return new
    return old


def _point_set(version: Version) -> VersionSet:
    from .spec import parse_spec

    return parse_spec(str(version))


def _intersect(requirements: list[Requirement]) -> VersionSet:
    result = requirements[0].set
    for requirement in requirements[1:]:
        result = result & requirement.set
    return result


def _excludes_all(requirement_set: VersionSet, versions) -> bool:
    return all(version not in requirement_set for version in versions)


def _minimal_core(requirements: list[Requirement], available) -> list[Requirement]:
    core = list(requirements)
    changed = True
    while changed:
        changed = False
        for requirement in list(core):
            trial = [item for item in core if item is not requirement]
            if trial and _excludes_all(_intersect(trial), available):
                core = trial
                changed = True
                break
    return core


def _version_key(version: Version):
    return (
        version.major,
        version.minor,
        version.patch,
        1 if version.channel is None else 0,
        version.channel or "",
        version.seq or 0,
    )


def solve(
    index: PackageIndex,
    roots: list[Dependency],
    context: dict[str, str] | None = None,
    locked: dict[str, Version] | None = None,
) -> Solution:
    return Solver(index, context or {}, locked or {}).solve(roots)
