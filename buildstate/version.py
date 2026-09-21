"""Semantic-version parsing for locally declared package versions.

Only the subset used by the build definition is supported:
``major.minor.patch`` with an optional dot-joined pre-release suffix.
Nothing is fetched from the network; versions are pure data parsed from
local JSON.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from functools import total_ordering


class VersionError(ValueError):
    """Raised when a version string is not ``major.minor.patch[-pre]``."""


def _parse_identifier(identifier: str) -> int | str:
    if identifier == "":
        raise VersionError("empty pre-release identifier")
    if identifier.isascii() and identifier.isdigit():
        if len(identifier) > 1 and identifier[0] == "0":
            raise VersionError(
                f"numeric pre-release identifier {identifier!r} has a leading zero"
            )
        return int(identifier)
    allowed = set(
        "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-"
    )
    if any(char not in allowed for char in identifier):
        raise VersionError(
            f"invalid character in pre-release identifier {identifier!r}"
        )
    return identifier


@total_ordering
@dataclass(frozen=True)
class Version:
    """A parseable, comparable ``major.minor.patch[-prerelease]`` version."""

    major: int
    minor: int
    patch: int
    prerelease: tuple[object, ...] = field(default=())

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str) or not text:
            raise VersionError("version must be a non-empty string")
        core, sep, suffix = text.partition("-")
        parts = core.split(".")
        if sep and suffix == "":
            raise VersionError("pre-release suffix is empty")
        if len(parts) != 3:
            raise VersionError(
                f"version {text!r} must have major.minor.patch core components"
            )
        numbers = []
        for part in parts:
            if not (part.isascii() and part.isdigit()):
                raise VersionError(f"version component {part!r} must be numeric")
            if len(part) > 1 and part[0] == "0":
                raise VersionError(f"version component {part!r} has a leading zero")
            numbers.append(int(part))
        prerelease: tuple[object, ...] = ()
        if sep:
            prerelease = tuple(_parse_identifier(item) for item in suffix.split("."))
        return cls(*numbers, prerelease)

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _key(self) -> tuple[int, int, int, int, tuple]:
        # No pre-release ranks above any pre-release (SemVer precedence).
        present = 0 if self.prerelease else 1
        rank = tuple(
            (0, value) if isinstance(value, int) else (1, value)
            for value in self.prerelease
        )
        return (self.major, self.minor, self.patch, present, rank)

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

    def __str__(self) -> str:
        core = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return core + "-" + ".".join(str(part) for part in self.prerelease)
        return core
