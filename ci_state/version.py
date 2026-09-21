"""Semantic version (major.minor.patch[-prerelease][+build]) parsing and ordering.

Only the local package catalog is consulted; this module never accesses the
network. Prerelease precedence follows SemVer 2.0:

* numeric identifiers compare numerically (3 < 12);
* alphanumeric identifiers compare lexically;
* numeric identifiers always rank below alphanumeric ones;
* a version WITHOUT a prerelease ranks ABOVE the same core with one
  (1.0.0 > 1.0.0-rc1);
* shorter prerelease lists rank below longer lists when they are a prefix
  (1.0.0-alpha < 1.0.0-alpha.1).
Build metadata (``+xyz``) is parsed but never affects precedence.
"""

from __future__ import annotations

import re
from typing import List, Sequence, Union

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
_IDENT_RE = re.compile(r"^[0-9A-Za-z-]+$")


class VersionError(ValueError):
    """Raised when a string is not a valid semantic version."""


PreIdent = Union[int, str]


def _cmp_pre_ident(left: PreIdent, right: PreIdent) -> int:
    if isinstance(left, int) and isinstance(right, int):
        return (left > right) - (left < right)
    if isinstance(left, int):
        return -1  # numeric always has lower precedence than alphanumeric
    if isinstance(right, int):
        return 1
    if left == right:
        return 0
    return -1 if left < right else 1


def _cmp_prerelease(left: Sequence[PreIdent], right: Sequence[PreIdent]) -> int:
    for a, b in zip(left, right):
        result = _cmp_pre_ident(a, b)
        if result != 0:
            return result
    if len(left) == len(right):
        return 0
    if not left:
        return 1  # absent prerelease outranks a present one
    if not right:
        return -1
    return -1 if len(left) < len(right) else 1


class Version:
    """Parsed semantic version supporting comparison and prerelease marking."""

    __slots__ = ("major", "minor", "patch", "prerelease", "build")

    def __init__(
        self,
        major: int,
        minor: int,
        patch: int,
        prerelease: Sequence[PreIdent] = (),
        build: str = "",
    ) -> None:
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease: List[PreIdent] = list(prerelease)
        self.build = build

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text.strip())
        if match is None:
            raise VersionError(f"invalid semantic version: {text!r}")
        prerelease: List[PreIdent] = []
        raw_pre = match.group("pre")
        if raw_pre is not None:
            for ident in raw_pre.split("."):
                if not _IDENT_RE.match(ident):
                    raise VersionError(f"invalid prerelease identifier: {ident!r}")
                if ident.isdigit():
                    prerelease.append(int(ident))
                else:
                    prerelease.append(ident)
        return cls(
            int(match.group("major")),
            int(match.group("minor")),
            int(match.group("patch")),
            prerelease,
            match.group("build") or "",
        )

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def core(self) -> str:
        return f"{self.major}.{self.minor}.{self.patch}"

    def _key(self):
        # Comparison key for prerelease uses a custom comparator; implement
        # ordering through __lt__ instead.
        return (self.major, self.minor, self.patch, tuple(self.prerelease))

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (
            self.major == other.major
            and self.minor == other.minor
            and self.patch == other.patch
            and _cmp_prerelease(self.prerelease, other.prerelease) == 0
        )

    def __lt__(self, other: "Version") -> bool:
        if (self.major, self.minor, self.patch) != (
            other.major,
            other.minor,
            other.patch,
        ):
            return (self.major, self.minor, self.patch) < (
                other.major,
                other.minor,
                other.patch,
            )
        return _cmp_prerelease(self.prerelease, other.prerelease) < 0

    def __le__(self, other: "Version") -> bool:
        return self == other or self < other

    def __gt__(self, other: "Version") -> bool:
        return other < self

    def __ge__(self, other: "Version") -> bool:
        return other <= self

    def __hash__(self) -> int:
        # Normalize numeric prerelease ids back to strings so 1 and "1" hash alike.
        pre_tuple = tuple(
            str(ident) if isinstance(ident, int) else ident
            for ident in self.prerelease
        )
        return hash((self.major, self.minor, self.patch, pre_tuple))

    def __repr__(self) -> str:
        return f"Version({self})"

    def __str__(self) -> str:
        text = self.core()
        if self.prerelease:
            text += "-" + ".".join(str(ident) for ident in self.prerelease)
        if self.build:
            text += "+" + self.build
        return text
