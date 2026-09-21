"""Semantic versions: major.minor.patch with optional prerelease."""

from __future__ import annotations

import functools
import re
from dataclasses import dataclass

_PATTERN = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


@functools.total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: str | None = None

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise ValueError(f"version must be a string, got {type(text).__name__}")
        match = _PATTERN.match(text)
        if match is None:
            raise ValueError(f"invalid version: {text!r}")
        major, minor, patch, prerelease = match.groups()
        return cls(int(major), int(minor), int(patch), prerelease)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return f"{base}-{self.prerelease}"
        return base

    def _prerelease_key(self) -> list[tuple[int, int, str]]:
        key = []
        for ident in self.prerelease.split("."):
            if ident.isdigit():
                key.append((0, int(ident), ""))
            else:
                key.append((1, 0, ident))
        return key

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        mine = (self.major, self.minor, self.patch)
        theirs = (other.major, other.minor, other.patch)
        if mine != theirs:
            return mine < theirs
        if self.prerelease == other.prerelease:
            return False
        if self.prerelease is None:
            return False
        if other.prerelease is None:
            return True
        return self._prerelease_key() < other._prerelease_key()
