"""Semantic versions ``major.minor.patch`` with optional pre-release tags.

Only local data is used; there is no network lookup.  Pre-release ordering
follows Semantic Versioning 2.0.0: numeric identifiers compare numerically,
alphanumeric identifiers lexically, numeric identifiers have lower
precedence than alphanumeric ones, and a version *with* a pre-release tag
has lower precedence than the same version without one.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
_IDENT_RE = re.compile(r"^(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)$")


class InvalidVersion(ValueError):
    """Raised when a version string is not a valid ``major.minor.patch`` value."""


def _ident_key(identifier: str) -> tuple[int, object]:
    if identifier.isdigit():
        return (0, int(identifier))
    return (1, identifier)


@dataclass(frozen=True, slots=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise InvalidVersion(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text.strip())
        if match is None:
            raise InvalidVersion(f"invalid version: {text!r}")
        prerelease: tuple[str, ...] = ()
        raw_pre = match.group("prerelease")
        if raw_pre is not None:
            identifiers = raw_pre.split(".")
            if any(identifier == "" or not _IDENT_RE.match(identifier) for identifier in identifiers):
                raise InvalidVersion(f"invalid pre-release identifier in {text!r}")
            prerelease = tuple(identifiers)
        return cls(
            major=int(match.group("major")),
            minor=int(match.group("minor")),
            patch=int(match.group("patch")),
            prerelease=prerelease,
        )

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _cmp_key(self) -> tuple[int, int, int, int, tuple[tuple[int, object], ...]]:
        # A missing pre-release tuple sorts *higher* (release beats pre-release).
        pre_flag = 1 if not self.prerelease else 0
        return (
            self.major,
            self.minor,
            self.patch,
            pre_flag,
            tuple(_ident_key(i) for i in self.prerelease),
        )

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp_key() < other._cmp_key()

    def __le__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp_key() <= other._cmp_key()

    def __gt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp_key() > other._cmp_key()

    def __ge__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp_key() >= other._cmp_key()

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return f"{base}-{'.'.join(self.prerelease)}"
        return base
