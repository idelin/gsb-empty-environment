"""Semantic version parsing, comparison, and simple constraint matching.

Versions follow ``major.minor.patch`` with an optional pre-release suffix
(``1.2.3-alpha.1``). Pre-release versions rank below the same release, and a
longer pre-release identifier chain outranks a prefix of it (semver.org).
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering
from typing import Iterable, Tuple, Union

from .errors import CapabilityResolverError

_VERSION_RE = re.compile(
    r"^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

_OPS = ("==", ">=", "<=", ">", "<", "!=")


class VersionError(CapabilityResolverError):
    """A version string or constraint is malformed."""


def _pre_key(ident: str) -> Tuple[int, object]:
    if ident.isdigit():
        # Numeric identifiers have lower precedence than non-numeric ones and
        # compare numerically (no leading-zero forms are rejected so that
        # ordering stays unambiguous).
        return (0, int(ident))
    return (1, ident)


@total_ordering
class Version:
    """A parsed ``major.minor.patch[-prerelease]`` version."""

    __slots__ = ("major", "minor", "patch", "prerelease", "_raw")

    def __init__(self, text: str) -> None:
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text.strip())
        if match is None:
            raise VersionError(f"invalid semantic version: {text!r}")
        major, minor, patch, pre = match.groups()
        self.major = int(major)
        self.minor = int(minor)
        self.patch = int(patch)
        if pre is not None:
            parts = pre.split(".")
            for part in parts:
                if part.isdigit() and len(part) > 1 and part.startswith("0"):
                    raise VersionError(
                        f"numeric pre-release identifier has a leading zero in {text!r}"
                    )
            self.prerelease = tuple(parts)
        else:
            self.prerelease = None
        self._raw = text.strip()

    @property
    def is_prerelease(self) -> bool:
        return self.prerelease is not None

    @property
    def core(self) -> Tuple[int, int, int]:
        return (self.major, self.minor, self.patch)

    def _key(self) -> Tuple[Tuple[int, int, int], int, Tuple]:
        # No pre-release ranks above any pre-release: use flag 1 when released.
        flag = 0 if self.prerelease is not None else 1
        prekey = (
            tuple(_pre_key(p) for p in self.prerelease)
            if self.prerelease is not None
            else ()
        )
        return (self.core, flag, prekey)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key()

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() < other._key()

    def __hash__(self) -> int:
        return hash(self._key())

    def __repr__(self) -> str:
        return f"Version({self._raw!r})"

    def __str__(self) -> str:
        if self.prerelease is None:
            return f"{self.major}.{self.minor}.{self.patch}"
        return f"{self.major}.{self.minor}.{self.patch}-{'.'.join(self.prerelease)}"


@dataclass(frozen=True)
class Constraint:
    """A single operator/version bound, e.g. ``>=1.2.0``."""

    op: str
    version: Version

    def satisfies(self, candidate: Version) -> bool:
        if self.op == "==":
            return candidate == self.version
        if self.op == "!=":
            return candidate != self.version
        if self.op == ">":
            return candidate > self.version
        if self.op == "<":
            return candidate < self.version
        if self.op == ">=":
            return candidate >= self.version
        if self.op == "<=":
            return candidate <= self.version
        raise VersionError(f"unsupported operator: {self.op!r}")

    def __str__(self) -> str:
        return f"{self.op}{self.version}"


def parse_constraint(spec: str) -> Constraint:
    """Parse a single constraint such as ``>=1.2.0`` or ``==2.0.0``."""
    text = spec.strip()
    for op in _OPS:
        if text.startswith(op):
            raw = text[len(op):].strip()
            return Constraint(op=op, version=Version(raw))
    # Bare versions mean an exact match.
    return Constraint(op="==", version=Version(text))


def satisfies_all(version: Version, constraints: Iterable[Constraint]) -> bool:
    return all(c.satisfies(version) for c in constraints)
