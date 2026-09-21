"""Version handling: strict ``major.minor.patch`` with optional pre-release.

No network access is involved anywhere; versions are only parsed and
validated locally.
"""
from __future__ import annotations

import re
from dataclasses import dataclass

_IDENT = r"(?:0|[1-9]\d*|[A-Za-z-][0-9A-Za-z-]*)"
_VERSION_RE = re.compile(
    rf"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-({_IDENT}(?:\.{_IDENT})*))?$"
)


class VersionError(ValueError):
    """Raised when a version string is not major.minor.patch[-prerelease]."""


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: str | None = None

    @classmethod
    def parse(cls, text: object) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text)
        if match is None:
            raise VersionError(
                f"invalid version {text!r}: expected major.minor.patch "
                "with optional -prerelease (e.g. 1.2.0 or 1.2.0-rc.1)"
            )
        major, minor, patch, prerelease = match.groups()
        return cls(int(major), int(minor), int(patch), prerelease)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        return f"{base}-{self.prerelease}" if self.prerelease is not None else base
