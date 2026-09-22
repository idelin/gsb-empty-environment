"""Semantic-style version parsing and constraint matching.

Versions use the form ``major.minor.patch`` with an optional pre-release
suffix, e.g. ``1.2.3`` or ``2.0.0-rc.1``.  Everything lives in the standard
library and no network access is performed.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
_IDENT_RE = re.compile(r"^[0-9A-Za-z-]+$")


class VersionError(ValueError):
    """Raised when a version or constraint string cannot be parsed."""


def _pre_key(identifier: str):
    # Numeric identifiers compare numerically and must not have leading zeros.
    if identifier.isdigit() and identifier != "0" and identifier.startswith("0"):
        raise VersionError(f"numeric pre-release identifier has leading zero: {identifier}")
    if not _IDENT_RE.match(identifier):
        raise VersionError(f"invalid pre-release identifier: {identifier}")
    return (0, int(identifier)) if identifier.isdigit() else (1, identifier)


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()
    build: str = ""

    @classmethod
    def parse(cls, text: str) -> "Version":
        candidate = text.strip()
        match = _VERSION_RE.match(candidate)
        if not match:
            raise VersionError(f"invalid version (expected major.minor.patch[-pre][+build]): {text!r}")
        pre = tuple(match.group("pre").split(".")) if match.group("pre") else ()
        for identifier in pre:
            _pre_key(identifier)
        return cls(
            major=int(match.group("major")),
            minor=int(match.group("minor")),
            patch=int(match.group("patch")),
            prerelease=pre,
            build=match.group("build") or "",
        )

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _order(self):
        # A missing pre-release ranks higher than any pre-release (1.0.0 > 1.0.0-rc.1).
        pre_key = (1,) if not self.prerelease else (0, tuple(_pre_key(i) for i in self.prerelease))
        return (self.major, self.minor, self.patch, pre_key)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._order() == other._order()

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._order() < other._order()

    def __hash__(self) -> int:
        return hash(self._order())

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        if self.build:
            base += "+" + self.build
        return base


_OPERATORS = ("==", ">=", "<=", ">", "<", "!=")


@dataclass(frozen=True)
class Clause:
    op: str
    version: Version
    raw: str

    def satisfies(self, candidate: Version) -> bool:
        if self.op == "==":
            return candidate == self.version
        if self.op == "!=":
            return candidate != self.version
        if self.op == ">=":
            return candidate >= self.version
        if self.op == "<=":
            return candidate <= self.version
        if self.op == ">":
            return candidate > self.version
        if self.op == "<":
            return candidate < self.version
        raise VersionError(f"unsupported operator: {self.op}")




@dataclass(frozen=True)
class Constraint:
    """A space-separated AND list of clauses, ``^``/``~`` ranges or a bare version.

    A bare version such as ``1.2.3`` means exactly that version.
    ``^`` follows the common compatible-range rule and ``~`` allows patch
    upgrades within the same minor.
    """

    clauses: tuple[Clause, ...]
    raw: str

    @classmethod
    def parse(cls, text: object) -> "Constraint":
        if not isinstance(text, str) or not text.strip():
            raise VersionError("constraint must be a non-empty string")
        raw = text.strip()
        pieces = raw.split()
        clauses: list[Clause] = []
        for piece in pieces:
            clauses.extend(_parse_piece(piece))
        if not clauses:
            raise VersionError(f"constraint contains no clauses: {raw!r}")
        return cls(clauses=tuple(clauses), raw=raw)

    def satisfies(self, candidate: Version) -> bool:
        if candidate.is_prerelease and not any(
            clause.version.is_prerelease
            and (clause.version.major, clause.version.minor, clause.version.patch)
            == (candidate.major, candidate.minor, candidate.patch)
            for clause in self.clauses
        ):
            return False
        return all(clause.satisfies(candidate) for clause in self.clauses)


def _parse_piece(piece: str) -> list[Clause]:
    if piece.startswith("^"):
        low = Version.parse(piece[1:])
        high = _caret_upper(low)
        return [Clause(">=", low, piece), Clause("<", high, piece)]
    if piece.startswith("~"):
        low = Version.parse(piece[1:])
        high = Version.parse(f"{low.major}.{low.minor + 1}.0")
        return [Clause(">=", low, piece), Clause("<", high, piece)]
    for op in _OPERATORS:
        if piece.startswith(op):
            return [Clause(op, Version.parse(piece[len(op):].strip()), piece)]
    # Bare version: exact match.
    return [Clause("==", Version.parse(piece), piece)]


def _caret_upper(low: Version) -> Version:
    if low.major > 0:
        return Version.parse(f"{low.major + 1}.0.0")
    if low.minor > 0:
        return Version.parse(f"0.{low.minor + 1}.0")
    return Version.parse(f"0.0.{low.patch + 1}")
