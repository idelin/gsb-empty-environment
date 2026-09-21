"""Backtracking resolver with a minimal-change objective.

Objective (lexicographic):
  1. minimize the number of changed packages relative to the lockfile
     (an upgrade, downgrade, addition or removal each counts as one change);
  2. among equally small solutions, prefer newer versions (packages are
     compared in alphabetical order, higher version wins).

Outcomes:
  - SOLVED: search space exhausted, the returned solution is optimal.
  - UNSATISFIABLE: search space exhausted, no solution exists (proven).
  - LIMIT: the state budget ran out; the result is incomplete. If a
    solution was found before the limit, it is reported as best-so-far
    and is NOT proven optimal.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .model import Lock, Repository, Requirements
from .version import Version

DEFAULT_MAX_STATES = 100_000
MAX_REPORTED_CONFLICTS = 10


class Status(Enum):
    SOLVED = "solved"
    UNSATISFIABLE = "unsatisfiable"
    LIMIT = "search-limit-reached"


class LockError(ValueError):
    """A locked package or version does not exist in the repository."""


class _LimitExceeded(Exception):
    pass


@dataclass(frozen=True)
class Source:
    """Where a constraint came from: the root requirements or a dependency."""

    kind: str  # "root" | "dependency"
    package: str = ""
    version: Version | None = None

    def describe(self) -> str:
        if self.kind == "root":
            return "root requirement"
        return f"{self.package} {self.version}"


@dataclass
class Reason:
    """Why a package ended up at its resolved version."""

    version: Version
    constraints: tuple  # of (Constraint, Source) active when assigned
    locked_rejection: str | None = None  # conflict found under the locked version


@dataclass
class Change:
    package: str
    kind: str  # "upgraded" | "downgraded" | "added" | "removed"
    old: Version | None
    new: Version | None


@dataclass
class SolveResult:
    status: Status
    resolution: dict  # name -> Version (empty unless a solution was found)
    reasons: dict  # name -> Reason
    changes: list  # of Change
    conflicts: list  # of str, most recent first
    states: int
    optimal: bool  # True iff the search space was fully explored


def compute_changes(lock: Lock, resolution: dict) -> list:
    changes = []
    for name in sorted(set(lock.pinned) | set(resolution)):
        old = lock.pinned.get(name)
        new = resolution.get(name)
        if old == new:
            continue
        if old is None:
            kind = "added"
        elif new is None:
            kind = "removed"
        elif new > old:
            kind = "upgraded"
        else:
            kind = "downgraded"
        changes.append(Change(name, kind, old, new))
    return changes


class Solver:
    def __init__(
        self,
        repository: Repository,
        lock: Lock,
        requirements: Requirements,
        *,
        max_states: int = DEFAULT_MAX_STATES,
        keep_orphans: bool = False,
    ):
        self.repo = repository
        self.lock = lock
        self.requirements = requirements
        self.max_states = max_states
        # Orphan policy: locked packages that become unreachable from the
        # root requirements are pruned from the new lock, unless
        # keep_orphans=True pins them at their locked versions.
        self.keep_orphans = keep_orphans
        self._states = 0
        self._best_key = None
        self._best = None  # (resolution, reasons)
        self._conflicts = []
        self._rank = {
            name: {v: i for i, v in enumerate(repository.versions(name))}
            for name in repository._packages
        }

    def validate_lock(self) -> None:
        errors = []
        for name, version in sorted(self.lock.pinned.items()):
            versions = self.repo.versions(name)
            if versions is None:
                errors.append(f"locked package {name!r} is not in the repository")
            elif version not in versions:
                errors.append(
                    f"locked version {name} {version} is not in the repository"
                )
        if errors:
            raise LockError("\n".join(errors))

    def solve(self) -> SolveResult:
        self.validate_lock()
        constraints = {
            name: [(constraint, Source("root"))]
            for name, constraint in self.requirements.constraints.items()
        }
        completed = True
        try:
            self._dfs({}, constraints, {})
        except _LimitExceeded:
            completed = False
        if self._best is None:
            status = Status.UNSATISFIABLE if completed else Status.LIMIT
            return SolveResult(
                status=status,
                resolution={},
                reasons={},
                changes=[],
                conflicts=self._recent_conflicts(),
                states=self._states,
                optimal=completed,
            )
        resolution, reasons = self._best
        status = Status.SOLVED if completed else Status.LIMIT
        return SolveResult(
            status=status,
            resolution=resolution,
            reasons=reasons,
            changes=compute_changes(self.lock, resolution),
            conflicts=self._recent_conflicts(),
            states=self._states,
            optimal=completed,
        )

    def _recent_conflicts(self) -> list:
        return list(reversed(self._conflicts[-MAX_REPORTED_CONFLICTS:]))

    # -- search ---------------------------------------------------------

    def _dfs(self, assignment: dict, constraints: dict, reasons: dict) -> None:
        self._states += 1
        if self._states > self.max_states:
            raise _LimitExceeded
        pending = [name for name in constraints if name not in assignment]
        if not pending:
            self._record(assignment, reasons)
            return
        # Most-constrained package first (fewest candidate versions).
        chosen, candidates = None, None
        for name in sorted(pending):
            cands = self._candidates(name, constraints[name])
            if candidates is None or len(cands) < len(candidates):
                chosen, candidates = name, cands
                if len(cands) <= 1:
                    break
        if not candidates:
            self._add_conflict(chosen, constraints[chosen])
            return
        locked = self.lock.pinned.get(chosen)
        ordered = sorted(candidates, reverse=True)
        if locked in ordered:
            # Try the locked version first: it is the zero-change candidate.
            ordered.remove(locked)
            ordered.insert(0, locked)
        locked_rejection = None
        for version in ordered:
            if self._best_key is not None:
                bound = self._change_lower_bound(assignment, chosen, version, locked)
                if bound > self._best_key[0]:
                    continue
            merged, failure = self._merge_dependencies(
                assignment, constraints, chosen, version
            )
            if merged is None:
                if version == locked:
                    locked_rejection = failure
                continue
            assignment[chosen] = version
            reasons[chosen] = Reason(
                version,
                tuple(constraints[chosen]),
                locked_rejection if version != locked else None,
            )
            if version == locked:
                mark = len(self._conflicts)
                self._dfs(assignment, merged, reasons)
                if len(self._conflicts) > mark:
                    locked_rejection = self._conflicts[mark]
            else:
                self._dfs(assignment, merged, reasons)
            del assignment[chosen]
            del reasons[chosen]

    def _candidates(self, name: str, constraint_list: list):
        versions = self.repo.versions(name)
        if versions is None:
            return []
        return [
            v
            for v in versions
            if all(constraint.allows(v) for constraint, _ in constraint_list)
        ]

    def _merge_dependencies(
        self, assignment: dict, constraints: dict, name: str, version: Version
    ):
        merged = {pkg: list(entries) for pkg, entries in constraints.items()}
        for dep, constraint in self.repo.dependencies(name, version).items():
            if dep in assignment and not constraint.allows(assignment[dep]):
                return None, (
                    f"{name} {version} requires {dep} {constraint}, but {dep} "
                    f"is already resolved to {assignment[dep]}"
                )
            if self.repo.versions(dep) is None:
                return None, (
                    f"{name} {version} requires {dep} {constraint}, but {dep} "
                    "is not in the repository"
                )
            merged.setdefault(dep, []).append(
                (constraint, Source("dependency", name, version))
            )
        return merged, None

    def _change_lower_bound(
        self, assignment: dict, name: str, version: Version, locked
    ) -> int:
        count = 0
        for pkg, ver in assignment.items():
            if self.lock.pinned.get(pkg) != ver:
                count += 1
        if locked != version:
            count += 1
        return count

    def _add_conflict(self, name: str, constraint_list: list) -> None:
        causes = "; ".join(
            f"{source.describe()} requires {name} {constraint}"
            for constraint, source in constraint_list
        )
        versions = self.repo.versions(name)
        if versions is None:
            message = f"{name}: package not in repository ({causes})"
        else:
            shown = ", ".join(str(v) for v in versions[-5:])
            message = (
                f"{name}: no available version satisfies {causes} "
                f"(available: {shown})"
            )
        self._conflicts.append(message)

    def _record(self, assignment: dict, reasons: dict) -> None:
        resolution = dict(assignment)
        if self.keep_orphans:
            for pkg, ver in self.lock.pinned.items():
                resolution.setdefault(pkg, ver)
        changes = sum(
            1 for pkg, ver in resolution.items() if self.lock.pinned.get(pkg) != ver
        )
        changes += sum(1 for pkg in self.lock.pinned if pkg not in resolution)
        key = (changes, self._version_key(resolution))
        if self._best_key is None or key < self._best_key:
            self._best_key = key
            self._best = (resolution, dict(reasons))

    def _version_key(self, resolution: dict) -> tuple:
        # Newer versions sort lower: rank is the ascending index, negated.
        return tuple(
            -self._rank[name][version] for name, version in sorted(resolution.items())
        )
