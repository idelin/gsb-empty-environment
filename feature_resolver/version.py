"""Semantic versions (major.minor.patch with optional prerelease) and requirements."""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

from .errors import VersionError

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


def _compare_prerelease(left: tuple[str, ...], right: tuple[str, ...]) -> int:
    if not left and not right:
        return 0
    if not left:
        return 1
    if not right:
        return -1
    for a, b in zip(left, right):
        if a == b:
            continue
        a_num, b_num = a.isdigit(), b.isdigit()
        if a_num and b_num:
            return -1 if int(a) < int(b) else 1
        if a_num:
            return -1
        if b_num:
            return 1
        return -1 if a < b else 1
    if len(left) == len(right):
        return 0
    return -1 if len(left) < len(right) else 1


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise VersionError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        major, minor, patch, pre = match.groups()
        prerelease = tuple(pre.split(".")) if pre else ()
        for ident in prerelease:
            if ident.isdigit() and len(ident) > 1 and ident.startswith("0"):
                raise VersionError(
                    f"numeric prerelease identifier {ident!r} must not have leading zeros"
                )
        return cls(int(major), int(minor), int(patch), prerelease)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (
            self.major,
            self.minor,
            self.patch,
            self.prerelease,
        ) == (other.major, other.minor, other.patch, other.prerelease)

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        self_core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if self_core != other_core:
            return self_core < other_core
        return _compare_prerelease(self.prerelease, other.prerelease) < 0

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.prerelease))


_OPERATORS = ("<=", ">=", "==", "!=", "<", ">", "=")


def _apply(op: str, bound: Version, version: Version) -> bool:
    if op == "==":
        return version == bound
    if op == "!=":
        return version != bound
    if op == "<":
        return version < bound
    if op == "<=":
        return version <= bound
    if op == ">":
        return version > bound
    return version >= bound


@dataclass(frozen=True)
class Requirement:
    """A comma-separated conjunction of comparators, e.g. ``>=1.0.0, <2.0.0``."""

    comparators: tuple[tuple[str, Version], ...] = ()

    @classmethod
    def parse(cls, text: str | None) -> "Requirement":
        if text is None:
            return cls(())
        if not isinstance(text, str):
            raise VersionError(f"requirement must be a string, got {type(text).__name__}")
        stripped = text.strip()
        if stripped in ("", "*"):
            return cls(())
        comparators = []
        for chunk in stripped.split(","):
            chunk = chunk.strip()
            op = "=="
            for candidate in _OPERATORS:
                if chunk.startswith(candidate):
                    op = "==" if candidate == "=" else candidate
                    chunk = chunk[len(candidate):].strip()
                    break
            comparators.append((op, Version.parse(chunk)))
        return cls(tuple(comparators))

    def matches(self, version: Version) -> bool:
        return all(_apply(op, bound, version) for op, bound in self.comparators)

    def __str__(self) -> str:
        if not self.comparators:
            return "*"
        return ", ".join(f"{op}{version}" for op, version in self.comparators)
