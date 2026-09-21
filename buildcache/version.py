"""Semantic-style versions: major.minor.patch with an optional pre-release tag."""
from __future__ import annotations

import re
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


class VersionError(ValueError):
    """Raised when a version string is not major.minor.patch[-prerelease]."""


@total_ordering
class Version:
    __slots__ = ("major", "minor", "patch", "prerelease")

    def __init__(self, major: int, minor: int, patch: int, prerelease=()):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = tuple(prerelease)

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise VersionError(
                f"invalid version {text!r}; expected major.minor.patch[-prerelease]"
            )
        prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(int(match.group(1)), int(match.group(2)),
                   int(match.group(3)), prerelease)

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _release(self):
        return (self.major, self.minor, self.patch)

    def __eq__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._release() == other._release() and self.prerelease == other.prerelease

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        if self._release() != other._release():
            return self._release() < other._release()
        return _compare_prerelease(self.prerelease, other.prerelease) < 0

    def __hash__(self):
        return hash((self._release(), self.prerelease))

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(self.prerelease)
        return text

    def __repr__(self) -> str:
        return f"Version({str(self)!r})"


def _compare_prerelease(left: tuple, right: tuple) -> int:
    # No prerelease beats any prerelease: 1.0.0 > 1.0.0-alpha
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
            return -1  # numeric identifiers sort before alphanumeric ones
        if b_num:
            return 1
        return -1 if a < b else 1
    if len(left) == len(right):
        return 0
    return -1 if len(left) < len(right) else 1
