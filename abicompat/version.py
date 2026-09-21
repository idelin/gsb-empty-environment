"""Semantic version parsing and ordering (SemVer 2.0.0 subset).

Version strings look like ``major.minor.patch[-prerelease][+build]``.
Build metadata is ignored for precedence; pre-release identifiers follow
the SemVer rules (numeric identifiers compare numerically and always rank
below the same core version without a pre-release).
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering
from typing import List, Tuple, Union

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)"
    r"\.(?P<minor>0|[1-9]\d*)"
    r"\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

PreId = Union[int, str]


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: Tuple[PreId, ...] = ()
    build: str = ""
    original: str = ""

    @staticmethod
    def parse(text: str) -> "Version":
        if not isinstance(text, str):
            raise ValueError(f"version must be a string, got {text!r}")
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(f"invalid semantic version: {text!r}")
        pre_text = match.group("prerelease")
        ids: List[PreId] = []
        if pre_text is not None:
            for identifier in pre_text.split("."):
                if identifier.isdigit():
                    if len(identifier) > 1 and identifier[0] == "0":
                        raise ValueError(
                            f"numeric pre-release identifier has leading zero: {text!r}"
                        )
                    ids.append(int(identifier))
                else:
                    ids.append(identifier)
        return Version(
            major=int(match.group("major")),
            minor=int(match.group("minor")),
            patch=int(match.group("patch")),
            prerelease=tuple(ids),
            build=match.group("build") or "",
            original=text,
        )

    @property
    def core(self) -> Tuple[int, int, int]:
        return (self.major, self.minor, self.patch)

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _order_key(self) -> Tuple[Tuple[int, int, int], int, Tuple[PreId, ...]]:
        # A version without a pre-release sorts above one with a pre-release,
        # so use 1 for release and 0 plus identifiers for pre-releases.
        return (self.core, 1 if not self.prerelease else 0, self.prerelease)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._order_key() == other._order_key()

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        left = self._order_key()
        right = other._order_key()
        if left[0] != right[0]:
            return left[0] < right[0]
        if left[1] != right[1]:
            return left[1] < right[1]
        for left_id, right_id in zip(left[2], right[2]):
            compared = _compare_pre_ids(left_id, right_id)
            if compared != 0:
                return compared < 0
        return len(left[2]) < len(right[2])

    def __hash__(self) -> int:
        return hash(self._order_key())

    def __str__(self) -> str:
        return self.original or f"{self.major}.{self.minor}.{self.patch}"


def _compare_pre_ids(left: PreId, right: PreId) -> int:
    if isinstance(left, int) and isinstance(right, int):
        return (left > right) - (left < right)
    if isinstance(left, int):  # numeric identifiers rank below alphanumerics
        return -1
    if isinstance(right, int):
        return 1
    if left == right:
        return 0
    return -1 if left < right else 1


def bump_kind(old: Version, new: Version) -> str:
    """Return ``major``/``minor``/``patch``/``prerelease``/``none``.

    Pre-release-only movement (same numeric core) is reported as
    ``prerelease``; moving from a pre-release to its released core is a
    patch-or-higher core change, not merely a prerelease move.
    """
    if old == new:
        return "none"
    if new.major != old.major:
        return "major"
    if new.minor != old.minor:
        return "minor"
    if new.patch != old.patch:
        return "patch"
    if old.is_prerelease and not new.is_prerelease:
        return "patch"
    return "prerelease"
