"""Semantic versions: major.minor.patch with optional prerelease."""

from __future__ import annotations

import re
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


class VersionError(ValueError):
    """Raised when a version string is not valid semver."""


def _prerelease_ident(part: str):
    return int(part) if part.isdigit() else part


def _prerelease_lt(a: tuple, b: tuple) -> bool:
    # A release (no prerelease) sorts after any prerelease of the same base.
    if not a:
        return False
    if not b:
        return True
    for x, y in zip(a, b):
        if x == y:
            continue
        x_num = isinstance(x, int)
        y_num = isinstance(y, int)
        if x_num and y_num:
            return x < y
        # Numeric identifiers sort lower than alphanumeric ones.
        if x_num:
            return True
        if y_num:
            return False
        return x < y
    return len(a) < len(b)


@total_ordering
class Version:
    __slots__ = ("major", "minor", "patch", "prerelease")

    def __init__(self, major: int, minor: int, patch: int, prerelease: tuple = ()):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = tuple(prerelease)

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise VersionError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        prerelease = ()
        if match.group(4) is not None:
            prerelease = tuple(_prerelease_ident(p) for p in match.group(4).split("."))
        return cls(
            int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease
        )

    def _base(self) -> tuple:
        return (self.major, self.minor, self.patch)

    def __eq__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._base() == other._base() and self.prerelease == other.prerelease

    def __lt__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        if self._base() != other._base():
            return self._base() < other._base()
        return _prerelease_lt(self.prerelease, other.prerelease)

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.prerelease))

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(str(p) for p in self.prerelease)
        return text

    def __repr__(self) -> str:
        return f"Version({self})"
