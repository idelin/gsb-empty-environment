"""SemVer (major.minor.patch) parsing and comparison.

Only the local manifest is used to determine package versions; nothing is
fetched from the network.

Supported shapes:
    1, "1", "1.2", "1.2.3"
A release may additionally carry a dot separated pre-release identifier,
either embedded in the version string ("1.2.3-rc.1") or via the manifest
field ``"prerelease": "rc.1"`` / ``"prerelease": true``.
"""

from __future__ import annotations

import re
from functools import total_ordering
from typing import Optional, Sequence, Union

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9][0-9]*)"
    r"(?:\.(?P<minor>0|[1-9][0-9]*))?"
    r"(?:\.(?P<patch>0|[1-9][0-9]*))?"
    r"(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
_IDENT_RE = re.compile(r"^[0-9A-Za-z-]+$")


class InvalidVersion(ValueError):
    """Raised when a version string cannot be parsed."""


@total_ordering
class Version:
    """An immutable major.minor.patch version with an optional pre-release."""

    __slots__ = ("major", "minor", "patch", "prerelease", "_pre_ids")

    def __init__(
        self,
        major: int,
        minor: int = 0,
        patch: int = 0,
        prerelease: Optional[Union[str, Sequence[str]]] = None,
    ) -> None:
        for name, value in (("major", major), ("minor", minor), ("patch", patch)):
            if not isinstance(value, int) or isinstance(value, bool) or value < 0:
                raise InvalidVersion(f"{name} must be a non-negative integer")
        pre_ids: Optional[tuple[str, ...]] = None
        if prerelease is not None:
            if isinstance(prerelease, str):
                pre_ids = self._validate_prerelease(prerelease)
            else:
                pre_ids = tuple(
                    part for ident in prerelease for part in self._validate_prerelease(ident)
                )
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = ".".join(pre_ids) if pre_ids is not None else None
        self._pre_ids = pre_ids

    @staticmethod
    def _validate_prerelease(tag: str) -> tuple[str, ...]:
        if not isinstance(tag, str) or not tag:
            raise InvalidVersion("prerelease tag must be a non-empty string")
        parts = tuple(tag.split("."))
        for part in parts:
            if not _IDENT_RE.match(part):
                raise InvalidVersion(f"invalid prerelease identifier: {part!r}")
        return parts

    @classmethod
    def parse(cls, value: Union["Version", int, str]) -> "Version":
        if isinstance(value, cls):
            return value
        if isinstance(value, bool):
            raise InvalidVersion(f"unsupported version: {value!r}")
        if isinstance(value, int):
            text = str(value)
        elif isinstance(value, str):
            text = value.strip()
        else:
            raise InvalidVersion(f"unsupported version: {value!r}")
        match = _VERSION_RE.match(text)
        if match is None:
            raise InvalidVersion(f"invalid version: {value!r}")
        return cls(
            int(match.group("major")),
            int(match.group("minor") or 0),
            int(match.group("patch") or 0),
            match.group("prerelease"),
        )

    @property
    def is_prerelease(self) -> bool:
        return self._pre_ids is not None

    @staticmethod
    def _pre_key(ids: tuple[str, ...]) -> tuple[tuple[int, object], ...]:
        # Numeric identifiers compare numerically; alphanumeric identifiers
        # compare lexically and always rank above numeric identifiers.
        key: list[tuple[int, object]] = []
        for ident in ids:
            if ident.isdigit():
                key.append((0, int(ident)))
            else:
                key.append((1, ident))
        return tuple(key)

    def _sort_key(self) -> tuple[int, int, int, int, object]:
        if self._pre_ids is None:
            return (self.major, self.minor, self.patch, 1, ())
        return (self.major, self.minor, self.patch, 0, self._pre_key(self._pre_ids))

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._sort_key() == other._sort_key()

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._sort_key() < other._sort_key()

    def __hash__(self) -> int:
        return hash(self._sort_key())

    def __repr__(self) -> str:
        return f"Version('{self}')"

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease is not None:
            base += f"-{self.prerelease}"
        return base
