"""Capability-based package resolver.

Selects exactly one implementation per capability from a local package
repository, honouring version constraints, mutual exclusions and target
constraints. All input comes from local JSON files; no network access.

Stable tie-break rules (fully deterministic):
  1. Capabilities are resolved in alphabetical order.
  2. Candidate providers are tried in order: higher version first,
     then lexicographically smaller package name.
  3. The first complete assignment found by backtracking wins, so the
     result only depends on the rules above, never on input ordering.
"""

from __future__ import annotations

import functools
import re
from dataclasses import dataclass, field


class ResolutionError(Exception):
    """Raised when no valid capability -> implementation mapping exists."""


_VERSION_RE = re.compile(
    r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?"
)

_CONSTRAINT_RE = re.compile(r"(>=|<=|==|!=|>|<)\s*(\S+)")


@functools.total_ordering
@dataclass(frozen=True)
class Version:
    """Semantic version: major.minor.patch with optional pre-release."""

    major: int
    minor: int
    patch: int
    prerelease: tuple = ()

    @classmethod
    def parse(cls, text) -> "Version":
        match = _VERSION_RE.fullmatch(str(text).strip())
        if match is None:
            raise ValueError(f"invalid version: {text!r}")
        prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease)

    def sort_key(self):
        # A release sorts after any of its pre-releases: 1.0.0 > 1.0.0-rc.1.
        # Numeric identifiers compare numerically and sort before textual ones.
        if not self.prerelease:
            pre_key = (1,)
        else:
            pre_key = (0,) + tuple(
                (0, int(part)) if part.isdigit() else (1, part)
                for part in self.prerelease
            )
        return (self.major, self.minor, self.patch, pre_key)

    def __lt__(self, other):
        return self.sort_key() < other.sort_key()

    def __str__(self):
        base = f"{self.major}.{self.minor}.{self.patch}"
        return f"{base}-{'.'.join(self.prerelease)}" if self.prerelease else base


_OPERATORS = {
    ">=": lambda v, o: v >= o,
    "<=": lambda v, o: v <= o,
    ">": lambda v, o: v > o,
    "<": lambda v, o: v < o,
    "==": lambda v, o: v == o,
    "!=": lambda v, o: v != o,
}


@dataclass(frozen=True)
class Constraint:
    """Comma-separated version clauses, e.g. ">=1.2.0,<2.0.0". Empty matches any."""

    clauses: tuple = ()

    @classmethod
    def parse(cls, text) -> "Constraint":
        if text is None:
            return cls()
        clauses = []
        for part in str(text).split(","):
            part = part.strip()
            if not part:
                continue
            match = _CONSTRAINT_RE.fullmatch(part)
            if match is None:
                raise ValueError(f"invalid constraint clause: {part!r}")
            clauses.append((match.group(1), Version.parse(match.group(2))))
        return cls(tuple(clauses))

    def allows(self, version: Version) -> bool:
        return all(_OPERATORS[op](version, other) for op, other in self.clauses)


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    provides: tuple = ()
    conflicts: tuple = ()
    targets: tuple = ()
    requires: dict = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data) -> "Package":
        return cls(
            name=data["name"],
            version=Version.parse(data["version"]),
            provides=tuple(data.get("provides", ())),
            conflicts=tuple(data.get("conflicts", ())),
            targets=tuple(data.get("targets", ())),
            requires={
                cap: Constraint.parse(c)
                for cap, c in data.get("requires", {}).items()
            },
        )

    def __str__(self):
        return f"{self.name}@{self.version}"


class Repository:
    """Local package index.

    Providers of each capability are pre-sorted by the stable tie-break
    order: higher version first, then lexicographically smaller name.
    """

    def __init__(self, packages):
        self.packages = list(packages)
        providers = {}
        for pkg in self.packages:
            for cap in pkg.provides:
                providers.setdefault(cap, []).append(pkg)
        for pkgs in providers.values():
            pkgs.sort(key=lambda p: p.name)
            pkgs.sort(key=lambda p: p.version, reverse=True)
        self.providers = providers


class Resolver:
    """Backtracking resolver with cycle protection."""

    def __init__(self, repository: Repository, target: str = ""):
        self.repository = repository
        self.target = target

    def resolve(self, requirements: dict) -> dict:
        """Return {capability: Package} or raise ResolutionError."""
        assignment = {}
        chosen = []
        caps = sorted(requirements)
        if not self._search(caps, 0, requirements, assignment, chosen, frozenset()):
            raise ResolutionError(
                "no valid provider selection for capabilities: " + ", ".join(caps)
            )
        return {cap: assignment[cap] for cap in sorted(assignment)}

    def _search(self, caps, index, requirements, assignment, chosen, visiting):
        if index == len(caps):
            return True
        cap = caps[index]
        if cap in assignment:
            # Provided as a side effect of an earlier pick; the requirement's
            # own version constraint must still hold, else backtrack.
            if not requirements[cap].allows(assignment[cap].version):
                return False
            return self._search(caps, index + 1, requirements, assignment, chosen, visiting)
        if cap in visiting:
            # Cyclic alternative chain: refuse to recurse forever.
            return False
        constraint = requirements[cap]
        for pkg in self.repository.providers.get(cap, []):
            if not self._eligible(pkg, constraint, assignment, chosen):
                continue
            saved_assignment, saved_chosen = dict(assignment), list(chosen)
            for provided in pkg.provides:
                assignment[provided] = pkg
            chosen.append(pkg)
            if self._satisfy_requires(pkg, requirements, assignment, chosen, visiting | {cap}) \
                    and self._search(caps, index + 1, requirements, assignment, chosen, visiting):
                return True
            assignment.clear()
            assignment.update(saved_assignment)
            chosen.clear()
            chosen.extend(saved_chosen)
        return False

    def _satisfy_requires(self, pkg, requirements, assignment, chosen, visiting):
        for dep_cap in sorted(pkg.requires):
            constraint = pkg.requires[dep_cap]
            if dep_cap in assignment:
                if not constraint.allows(assignment[dep_cap].version):
                    return False
                continue
            if dep_cap in visiting:
                return False
            if not self._resolve_dependency(
                dep_cap, constraint, requirements, assignment, chosen, visiting
            ):
                return False
        return True

    def _resolve_dependency(self, dep_cap, constraint, requirements, assignment, chosen, visiting):
        outer = requirements.get(dep_cap)
        for cand in self.repository.providers.get(dep_cap, []):
            if outer is not None and not outer.allows(cand.version):
                continue
            if not self._eligible(cand, constraint, assignment, chosen):
                continue
            saved_assignment, saved_chosen = dict(assignment), list(chosen)
            for provided in cand.provides:
                assignment[provided] = cand
            chosen.append(cand)
            if self._satisfy_requires(cand, requirements, assignment, chosen, visiting | {dep_cap}):
                return True
            assignment.clear()
            assignment.update(saved_assignment)
            chosen.clear()
            chosen.extend(saved_chosen)
        return False

    def _eligible(self, pkg, constraint, assignment, chosen):
        if not constraint.allows(pkg.version):
            return False
        if self.target and pkg.targets and self.target not in pkg.targets:
            return False
        # Each capability keeps a single provider.
        if any(provided in assignment for provided in pkg.provides):
            return False
        for other in chosen:
            if other.name == pkg.name:
                return False  # at most one version per package name
            if other.name in pkg.conflicts or pkg.name in other.conflicts:
                return False
        return True
