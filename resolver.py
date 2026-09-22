"""Local JSON dependency resolver.

Reads a package index and root requirements from a local JSON file (no
network access), then picks a consistent version set. Packages are
considered in lexicographic name order and each gets the newest version
that satisfies every constraint (including transitive ones). Supports
ranges (``>=1.0.0,<2.0.0``), exclusion points (``!=1.5.0``), exact pins
and wildcards. Prerelease versions are only candidates when explicitly
allowed. On failure a ConflictError carries the reviewable constraint
set that no available version can satisfy.
"""

from __future__ import annotations

import json
import re
import sys
from dataclasses import dataclass
from functools import total_ordering

VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$"
)


@total_ordering
class Version:
    """A ``major.minor.patch`` version with an optional prerelease tag."""

    def __init__(self, major: int, minor: int, patch: int, prerelease: str | None = None):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = prerelease

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(f"invalid version: {text!r}")
        major, minor, patch, prerelease = match.groups()
        return cls(int(major), int(minor), int(patch), prerelease)

    @property
    def is_prerelease(self) -> bool:
        return self.prerelease is not None

    def _prerelease_key(self):
        # A release sorts after any prerelease of the same base version.
        if self.prerelease is None:
            return (1,)
        key = []
        for ident in self.prerelease.split("."):
            if ident.isdigit():
                key.append((0, int(ident), ""))
            else:
                key.append((1, 0, ident))
        return (0, tuple(key))

    def _key(self):
        return (self.major, self.minor, self.patch, self._prerelease_key())

    def __eq__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key()

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() < other._key()

    def __hash__(self):
        return hash(self._key())

    def __str__(self):
        base = f"{self.major}.{self.minor}.{self.patch}"
        return f"{base}-{self.prerelease}" if self.prerelease else base

    def __repr__(self):
        return f"Version({self})"


_OPS = ("<=", ">=", "!=", "==", "<", ">", "=")


@dataclass(frozen=True)
class Clause:
    """A single comparison such as ``>=1.2.0`` or ``!=1.5.0``."""

    op: str  # one of '*', '==', '!=', '<', '<=', '>', '>='
    version: Version | None
    raw: str

    def matches(self, version: Version) -> bool:
        if self.op == "*":
            return True
        if self.op == "==":
            return version == self.version
        if self.op == "!=":
            return version != self.version
        if self.op == "<":
            return version < self.version
        if self.op == "<=":
            return version <= self.version
        if self.op == ">":
            return version > self.version
        if self.op == ">=":
            return version >= self.version
        raise ValueError(f"unknown operator: {self.op}")

    def mentions_prerelease(self) -> bool:
        return self.version is not None and self.version.is_prerelease


def parse_clauses(spec: str) -> tuple[Clause, ...]:
    """Parse a comma-separated constraint spec into clauses (ANDed)."""
    spec = (spec or "").strip()
    if spec in ("", "*"):
        return (Clause("*", None, "*"),)
    clauses = []
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        op, _, rest = "=", "", part
        for candidate in _OPS:
            if part.startswith(candidate):
                op, rest = candidate, part[len(candidate):].strip()
                break
        else:
            op, rest = "==", part
        if op == "=":
            op = "=="
        if rest == "*":
            clauses.append(Clause("*", None, part))
            continue
        clauses.append(Clause(op, Version.parse(rest), part))
    if not clauses:
        return (Clause("*", None, "*"),)
    return tuple(clauses)


@dataclass
class SourcedConstraint:
    """A constraint together with who imposed it (for conflict reports)."""

    clauses: tuple[Clause, ...]
    raw: str
    source: str

    def matches(self, version: Version) -> bool:
        return all(clause.matches(version) for clause in self.clauses)


class ConflictError(Exception):
    """Raised when no version of a package satisfies all its constraints."""

    def __init__(self, package: str, constraints: list[SourcedConstraint],
                 available: list[Version]):
        self.package = package
        self.constraints = constraints
        self.available = available
        super().__init__(self._build_message())

    def _build_message(self) -> str:
        lines = [f"conflict: no version of '{self.package}' satisfies all constraints"]
        for sc in self.constraints:
            lines.append(f"  - {self.package} {sc.raw or '*'} (required by {sc.source})")
        available = ", ".join(str(v) for v in self.available) or "(none)"
        lines.append(f"  available versions: {available}")
        return "\n".join(lines)

    def to_dict(self) -> dict:
        return {
            "package": self.package,
            "constraints": [
                {"constraint": sc.raw or "*", "required_by": sc.source}
                for sc in self.constraints
            ],
            "available_versions": [str(v) for v in self.available],
        }


def _load_index(manifest: dict) -> dict[str, dict[Version, dict[str, str]]]:
    packages: dict[str, dict[Version, dict[str, str]]] = {}
    for name, versions in manifest.get("packages", {}).items():
        version_map = {}
        for version_text, deps in versions.items():
            version_map[Version.parse(version_text)] = dict(deps or {})
        packages[name] = version_map
    return packages


def _prerelease_allowed(name: str, constraints: list[SourcedConstraint],
                        allow_all: bool, allow_set: set[str]) -> bool:
    if allow_all or name in allow_set:
        return True
    # A constraint that explicitly references a prerelease opts the
    # package into prerelease candidates.
    return any(
        clause.mentions_prerelease()
        for sc in constraints
        for clause in sc.clauses
    )


def resolve(manifest: dict) -> dict[str, Version]:
    """Resolve the manifest into a {package: Version} mapping.

    Raises ConflictError with the offending constraint set on failure.
    """
    packages = _load_index(manifest)
    roots = manifest.get("roots", {})
    if not roots:
        raise ValueError("manifest has no 'roots'")

    allow = manifest.get("allow_prerelease", [])
    allow_all = allow is True or (isinstance(allow, list) and "*" in allow)
    allow_set = set(allow) if isinstance(allow, list) else set()

    constraints: dict[str, list[SourcedConstraint]] = {}
    for name, spec in roots.items():
        constraints.setdefault(name, []).append(
            SourcedConstraint(parse_clauses(spec), spec or "*", "root")
        )

    selected: dict[str, Version] = {}
    failures: list[ConflictError] = []

    def candidates(name: str) -> list[Version]:
        cons = constraints.get(name, [])
        versions = packages.get(name, {})
        allow_pre = _prerelease_allowed(name, cons, allow_all, allow_set)
        result = [
            v for v in versions
            if (allow_pre or not v.is_prerelease)
            and all(sc.matches(v) for sc in cons)
        ]
        return sorted(result, reverse=True)

    def record_failure(name: str) -> None:
        available = sorted(packages.get(name, {}), reverse=True)
        failures.append(ConflictError(name, list(constraints.get(name, [])), available))

    def search() -> bool:
        unresolved = [n for n in constraints if n not in selected]
        if not unresolved:
            return True
        name = min(unresolved)  # lexicographic package order
        options = candidates(name)
        if not options:
            record_failure(name)
            return False
        for version in options:
            deps = packages[name][version]
            added: list[tuple[str, SourcedConstraint]] = []
            compatible = True
            for dep, spec in deps.items():
                sc = SourcedConstraint(
                    parse_clauses(spec), spec or "*", f"{name}@{version}"
                )
                if dep in selected:
                    # Cycle or shared dep: check the already chosen version.
                    if not sc.matches(selected[dep]):
                        available = sorted(packages.get(dep, {}), reverse=True)
                        failures.append(ConflictError(
                            dep, constraints.get(dep, []) + [sc], available
                        ))
                        compatible = False
                        break
                else:
                    added.append((dep, sc))
            if not compatible:
                continue
            selected[name] = version
            for dep, sc in added:
                constraints.setdefault(dep, []).append(sc)
            if search():
                return True
            for dep, _ in reversed(added):
                constraints[dep].pop()
            del selected[name]
        return False

    if not search():
        raise failures[-1]
    return dict(selected)


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} <manifest.json>", file=sys.stderr)
        return 2
    with open(argv[1], "r", encoding="utf-8") as fh:
        manifest = json.load(fh)
    try:
        resolved = resolve(manifest)
    except ConflictError as exc:
        print(json.dumps({"status": "conflict", "detail": exc.to_dict()},
                         indent=2, ensure_ascii=False))
        print(file=sys.stderr)
        print(str(exc), file=sys.stderr)
        return 1
    output = {
        "status": "resolved",
        "versions": {name: str(resolved[name]) for name in sorted(resolved)},
    }
    print(json.dumps(output, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
