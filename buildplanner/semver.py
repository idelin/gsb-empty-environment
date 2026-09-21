"""Semantic version parsing: major.minor.patch with optional pre-release.

Only the standard library is used. No network access anywhere.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

_NUMERIC_RE = re.compile(r"^(0|[1-9]\d*)$")


class VersionError(ValueError):
    """Raised when a version string is not valid major.minor.patch[-prerelease]."""


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text)
        if not match:
            raise VersionError(
                f"invalid version {text!r}: expected major.minor.patch with "
                "optional '-prerelease' (dot-separated alphanumerics)"
            )
        major, minor, patch, pre = match.groups()
        prerelease: tuple[str, ...] = ()
        if pre is not None:
            identifiers = pre.split(".")
            for ident in identifiers:
                if ident.isdigit() and not _NUMERIC_RE.match(ident):
                    raise VersionError(
                        f"invalid version {text!r}: numeric pre-release "
                        f"identifier {ident!r} must not have leading zeros"
                    )
            prerelease = tuple(identifiers)
        return cls(int(major), int(minor), int(patch), prerelease)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return base + "-" + ".".join(self.prerelease)
        return base

    def _key(self):
        # A release sorts after any of its pre-releases (semver 2.0.0 item 11).
        if self.prerelease:
            pre_key = tuple(
                (0, int(ident)) if ident.isdigit() else (1, ident)
                for ident in self.prerelease
            )
            return (self.major, self.minor, self.patch, 0, pre_key)
        return (self.major, self.minor, self.patch, 1, ())

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() < other._key()

    def __le__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() <= other._key()

    def __gt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() > other._key()

    def __ge__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() >= other._key()
