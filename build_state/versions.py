"""SemVer-like major.minor.patch[-prerelease] parsing and ordering.

Only the local rules used by the build tracker are implemented; nothing is
downloaded. A version with an empty pre-release tuple is a *release* and ranks
higher than any pre-release of the same major.minor.patch.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Sequence

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9][0-9]*)\."
    r"(?P<minor>0|[1-9][0-9]*)\."
    r"(?P<patch>0|[1-9][0-9]*)"
    r"(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

_IDENT_RE = re.compile(r"^[0-9A-Za-z-]+$")
_NUMERIC_RE = re.compile(r"^[0-9]+$")


class VersionError(ValueError):
    """Raised when a version string does not match major.minor.patch[-pre]."""


def _compare_ident(left: str, right: str) -> int:
    left_num = bool(_NUMERIC_RE.match(left))
    right_num = bool(_NUMERIC_RE.match(right))
    if left_num and right_num:
        a, b = int(left), int(right)
        return (a > b) - (a < b)
    if left_num != right_num:
        # Numeric identifiers always have lower precedence than non-numeric.
        return -1 if left_num else 1
    return (left > right) - (left < right)


def compare_prerelease(left: Sequence[str], right: Sequence[str]) -> int:
    for a, b in zip(left, right):
        cmp = _compare_ident(a, b)
        if cmp:
            return cmp
    if len(left) == len(right):
        return 0
    # A shorter prerelease field ranks lower (1.0.0-alpha < 1.0.0-alpha.1).
    return -1 if len(left) < len(right) else 1


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    @property
    def core(self) -> tuple[int, int, int]:
        return self.major, self.minor, self.patch

    def compare_to(self, other: "Version") -> int:
        if self.core != other.core:
            return -1 if self.core < other.core else 1
        if not self.prerelease and not other.prerelease:
            return 0
        # Release > prerelease on the same core.
        if not self.prerelease:
            return 1
        if not other.prerelease:
            return -1
        return compare_prerelease(self.prerelease, other.prerelease)

    def __lt__(self, other: "Version") -> bool:
        return self.compare_to(other) < 0

    def __le__(self, other: "Version") -> bool:
        return self.compare_to(other) <= 0

    def __gt__(self, other: "Version") -> bool:
        return self.compare_to(other) > 0

    def __ge__(self, other: "Version") -> bool:
        return self.compare_to(other) >= 0

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return f"{base}-{'.'.join(self.prerelease)}"
        return base


def parse_version(text: str) -> Version:
    if not isinstance(text, str):
        raise VersionError(f"version must be a string, got {type(text).__name__}")
    match = _VERSION_RE.match(text.strip())
    if not match:
        raise VersionError(f"invalid version: {text!r}")
    prerelease_text = match.group("prerelease")
    if prerelease_text is not None:
        parts = tuple(prerelease_text.split("."))
        for part in parts:
            if not _IDENT_RE.match(part):
                raise VersionError(f"invalid prerelease identifier in {text!r}")
            if _NUMERIC_RE.match(part) and len(part) > 1 and part[0] == "0":
                raise VersionError(f"numeric prerelease must not have leading zeros in {text!r}")
    else:
        parts = ()
    return Version(
        major=int(match.group("major")),
        minor=int(match.group("minor")),
        patch=int(match.group("patch")),
        prerelease=parts,
    )
