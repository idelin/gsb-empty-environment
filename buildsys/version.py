"""Semantic versions (major.minor.patch with optional prerelease) and
version constraints. Standard library only."""

from __future__ import annotations

import functools
import re
from dataclasses import dataclass

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

_CLAUSE_RE = re.compile(r"^(==|!=|>=|<=|>|<)?(.+)$")


def _cmp_identifier(left: str, right: str) -> int:
    left_num = left.isdigit()
    right_num = right.isdigit()
    if left_num and right_num:
        return (int(left) > int(right)) - (int(left) < int(right))
    if left_num != right_num:
        # Numeric identifiers have lower precedence than alphanumeric.
        return -1 if left_num else 1
    return (left > right) - (left < right)


def _prerelease_lt(left: tuple, right: tuple) -> bool:
    if not left and not right:
        return False
    if not left:
        return False  # a release outranks any of its prereleases
    if not right:
        return True
    for l_ident, r_ident in zip(left, right):
        if l_ident == r_ident:
            continue
        return _cmp_identifier(l_ident, r_ident) < 0
    return len(left) < len(right)


@functools.total_ordering
@dataclass(frozen=True)
class Version:
    """major.minor.patch with an optional dot-separated prerelease."""

    major: int
    minor: int
    patch: int
    prerelease: tuple = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(
                f"invalid version {text!r}: want major.minor.patch[-prerelease]"
            )
        prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(
            int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease
        )

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def _core(self) -> tuple:
        return (self.major, self.minor, self.patch)

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        if self._core() != other._core():
            return self._core() < other._core()
        return _prerelease_lt(self.prerelease, other.prerelease)


class Constraint:
    """A conjunction of comparison clauses, e.g. ``>=1.2.0 <2.0.0``."""

    _OPS = {
        "==": lambda v, c: v == c,
        "!=": lambda v, c: v != c,
        ">=": lambda v, c: v >= c,
        "<=": lambda v, c: v <= c,
        ">": lambda v, c: v > c,
        "<": lambda v, c: v < c,
    }

    def __init__(self, clauses: tuple = ()):
        # clauses: tuple of (op_string, Version)
        self._clauses = clauses

    @classmethod
    def any(cls) -> "Constraint":
        return cls(())

    @classmethod
    def parse(cls, text: str) -> "Constraint":
        text = (text or "").strip()
        if text in ("", "*"):
            return cls.any()
        clauses = []
        for token in re.split(r"[,\s]+", text):
            if not token:
                continue
            match = _CLAUSE_RE.match(token)
            op, ver_text = match.group(1) or "==", match.group(2)
            clauses.append((op, Version.parse(ver_text)))
        return cls(tuple(clauses))

    def matches(self, version: Version) -> bool:
        return all(self._OPS[op](version, ver) for op, ver in self._clauses)

    def __str__(self) -> str:
        if not self._clauses:
            return "*"
        return " ".join(f"{op}{ver}" for op, ver in self._clauses)
