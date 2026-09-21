"""Semantic version parsing: major.minor.patch with optional prerelease.

Versions are parsed and compared locally only; no network access is
performed to resolve packages.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

_IDENT = r"(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)"
_SEMVER_RE = re.compile(
    rf"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    rf"(?:-({_IDENT}(?:\.{_IDENT})*))?$"
)


@total_ordering
@dataclass(frozen=True)
class Version:
    """A semantic version such as ``1.4.0`` or ``1.4.0-rc.1``."""

    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise ValueError(
                f"version must be a string, got {type(text).__name__}"
            )
        match = _SEMVER_RE.match(text)
        if not match:
            raise ValueError(
                f"invalid version {text!r}: expected "
                "major.minor.patch[-prerelease]"
            )
        major, minor, patch, pre = match.groups()
        prerelease = tuple(pre.split(".")) if pre else ()
        return cls(int(major), int(minor), int(patch), prerelease)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def _pre_key(self) -> tuple:
        # A release sorts after any of its prereleases; numeric identifiers
        # sort below alphanumeric ones and compare numerically.
        if not self.prerelease:
            return (1,)
        return (0,) + tuple(
            (0, int(ident)) if ident.isdigit() else (1, ident)
            for ident in self.prerelease
        )

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        self_key = (self.major, self.minor, self.patch, self._pre_key())
        other_key = (other.major, other.minor, other.patch, other._pre_key())
        return self_key < other_key
