"""SemVer-like versions: major.minor.patch with optional pre-release.

Pure standard library. Ordering follows semver.org rules:
a pre-release version sorts before the plain release, numeric
identifiers compare numerically and below alphanumeric ones.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$"
)


def _cmp_identifier(left: str, right: str) -> int:
    left_num, right_num = left.isdigit(), right.isdigit()
    if left_num and right_num:
        return (int(left) > int(right)) - (int(left) < int(right))
    if left_num:
        return -1  # numeric identifiers sort below alphanumeric
    if right_num:
        return 1
    return (left > right) - (left < right)


def _cmp_prerelease(left: tuple[str, ...], right: tuple[str, ...]) -> int:
    if not left and not right:
        return 0
    if not left:
        return 1  # plain release is greater than any pre-release
    if not right:
        return -1
    for a, b in zip(left, right):
        cmp = _cmp_identifier(a, b)
        if cmp:
            return cmp
    return (len(left) > len(right)) - (len(left) < len(right))


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(
                f"invalid version {text!r}: want major.minor.patch[-pre.release]"
            )
        pre = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(int(match.group(1)), int(match.group(2)), int(match.group(3)), pre)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core != other_core:
            return core < other_core
        return _cmp_prerelease(self.prerelease, other.prerelease) < 0

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (self.major, self.minor, self.patch, self.prerelease) == (
            other.major,
            other.minor,
            other.patch,
            other.prerelease,
        )

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.prerelease))


_OPS = (">=", "<=", "==", "!=", ">", "<")


class VersionConstraint:
    """Space-separated clauses, e.g. ``>=1.0.0 <2.0.0``. Bare ``x.y.z`` means ==."""

    def __init__(self, clauses: tuple[tuple[str, Version], ...]):
        self.clauses = clauses

    @classmethod
    def parse(cls, text: str) -> "VersionConstraint":
        text = text.strip()
        if text in ("", "*"):
            return cls(())
        clauses = []
        for part in text.split():
            for op in _OPS:
                if part.startswith(op):
                    clauses.append((op, Version.parse(part[len(op):])))
                    break
            else:
                clauses.append(("==", Version.parse(part)))
        return cls(tuple(clauses))

    def allows(self, version: Version) -> bool:
        for op, bound in self.clauses:
            if op == ">=" and not version >= bound:
                return False
            if op == "<=" and not version <= bound:
                return False
            if op == ">" and not version > bound:
                return False
            if op == "<" and not version < bound:
                return False
            if op == "==" and not version == bound:
                return False
            if op == "!=" and not version != bound:
                return False
        return True

    def __str__(self) -> str:
        return " ".join(f"{op}{ver}" for op, ver in self.clauses) or "*"
