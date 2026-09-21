"""Semantic versions of the form major.minor.patch with optional prerelease."""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


def _compare_identifier(left: str, right: str) -> int:
    left_num, right_num = left.isdigit(), right.isdigit()
    if left_num and right_num:
        return (int(left) > int(right)) - (int(left) < int(right))
    if left_num:
        return -1
    if right_num:
        return 1
    return (left > right) - (left < right)


def compare_prerelease(left: tuple[str, ...], right: tuple[str, ...]) -> int:
    """Order prerelease identifier lists per semver: release > prerelease."""
    if not left and not right:
        return 0
    if not left:
        return 1
    if not right:
        return -1
    for a, b in zip(left, right):
        result = _compare_identifier(a, b)
        if result:
            return result
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
            raise ValueError(f"invalid version: {text!r}")
        major, minor, patch, pre = match.groups()
        return cls(
            int(major),
            int(minor),
            int(patch),
            tuple(pre.split(".")) if pre else (),
        )

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core != other_core:
            return core < other_core
        return compare_prerelease(self.prerelease, other.prerelease) < 0

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(self.prerelease)
        return text
