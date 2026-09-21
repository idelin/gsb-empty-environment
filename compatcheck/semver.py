"""Semantic version handling: major.minor.patch with optional pre-release.

Only parsing, ordering and bump-level classification live here; no network
access is involved anywhere in the package.
"""
from __future__ import annotations

import re
from functools import total_ordering

_SEMVER_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?$"
)

_BUMP_ORDER = {"none": 0, "patch": 1, "minor": 2, "major": 3}


class VersionError(ValueError):
    """Raised when a version string is not valid semver."""


@total_ordering
class Version:
    """A parsed semantic version."""

    __slots__ = ("major", "minor", "patch", "prerelease")

    def __init__(self, major: int, minor: int, patch: int, prerelease: tuple = ()):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = tuple(prerelease)

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _SEMVER_RE.match(text.strip()) if isinstance(text, str) else None
        if not match:
            raise VersionError(f"invalid semantic version: {text!r}")
        prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease)

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def core(self) -> tuple:
        return (self.major, self.minor, self.patch)

    def _pre_key(self) -> tuple:
        # A version without a pre-release sorts after any pre-release.
        if not self.prerelease:
            return (1,)
        key = [0]
        for ident in self.prerelease:
            if ident.isdigit():
                key.append((0, int(ident), ""))
            else:
                key.append((1, 0, ident))
        return tuple(key)

    def __eq__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self.core() == other.core() and self._pre_key() == other._pre_key()

    def __lt__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        if self.core() != other.core():
            return self.core() < other.core()
        return self._pre_key() < other._pre_key()

    def __hash__(self) -> int:
        return hash((self.core(), self.prerelease))

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def __repr__(self) -> str:
        return f"Version({str(self)!r})"


def bump_level(old: Version, new: Version) -> str:
    """Classify the version step from ``old`` to ``new``.

    Returns one of ``major``, ``minor``, ``patch``, ``prerelease`` (same core,
    only the pre-release part changed) or ``none``.
    """
    if old.major != new.major:
        return "major"
    if old.minor != new.minor:
        return "minor"
    if old.patch != new.patch:
        return "patch"
    if old.prerelease != new.prerelease:
        return "prerelease"
    return "none"


def bump_satisfies(actual: str, required: str) -> bool:
    """True when ``actual`` bump level is at least as strong as ``required``."""
    return _BUMP_ORDER[actual] >= _BUMP_ORDER[required]
