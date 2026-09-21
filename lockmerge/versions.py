"""Semantic version and constraint handling (major.minor.patch[-prerelease]).

Pure standard library. Versions follow semver 2.0 ordering rules:
a pre-release version sorts before the corresponding release.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-((?:0|[1-9]\d*|[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|[A-Za-z-][0-9A-Za-z-]*))*))?$"
)

_COMPARATOR_RE = re.compile(r"^(>=|<=|==|!=|>|<|=|~|\^)?\s*(\S+)$")


class VersionError(ValueError):
    """Raised when a version or constraint string is malformed."""


def _split_prerelease(tag: str) -> tuple:
    parts = []
    for ident in tag.split("."):
        if ident.isdigit():
            parts.append((0, int(ident), ""))
        else:
            parts.append((1, 0, ident))
    return tuple(parts)


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple = ()  # tuple of (rank, num, text) identifiers, empty = release

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"version must be a string, got {type(text).__name__}")
        m = _VERSION_RE.match(text.strip())
        if not m:
            raise VersionError(f"invalid version: {text!r} (expected major.minor.patch[-prerelease])")
        major, minor, patch, pre = m.groups()
        return cls(int(major), int(minor), int(patch), _split_prerelease(pre) if pre else ())

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            tags = []
            for rank, num, text in self.prerelease:
                tags.append(str(num) if rank == 0 else text)
            return base + "-" + ".".join(tags)
        return base

    def _key(self):
        # A release (empty prerelease) sorts after any prerelease of the same x.y.z.
        pre_key = (1,) if not self.prerelease else (0,) + self.prerelease
        return (self.major, self.minor, self.patch, pre_key)

    def __lt__(self, other: "Version") -> bool:
        return self._key() < other._key()

    def __le__(self, other: "Version") -> bool:
        return self._key() <= other._key()

    def __gt__(self, other: "Version") -> bool:
        return self._key() > other._key()

    def __ge__(self, other: "Version") -> bool:
        return self._key() >= other._key()


class Constraint:
    """A conjunction of comparators, e.g. ``>=1.2.0 <2.0.0``.

    Supported operators: ``>=`` ``<=`` ``>`` ``<`` ``==`` ``!=`` ``=`` plus
    caret (``^1.2.3``) and tilde (``~1.2.3``) ranges. A bare version means
    exact equality. An empty string matches any version.
    """

    def __init__(self, raw: str):
        if not isinstance(raw, str):
            raise VersionError(f"constraint must be a string, got {type(raw).__name__}")
        self.raw = raw.strip()
        self._comparators = []  # list of (op, Version)
        if not self.raw or self.raw == "*":
            return
        for token in re.split(r"[,\s]+", self.raw):
            if not token:
                continue
            m = _COMPARATOR_RE.match(token)
            if not m:
                raise VersionError(f"invalid comparator: {token!r}")
            op, ver_text = m.groups()
            version = Version.parse(ver_text)
            op = op or "=="
            if op == "=":
                op = "=="
            if op == "^":
                self._comparators.append((">=", version))
                if version.major > 0:
                    upper = Version(version.major + 1, 0, 0)
                elif version.minor > 0:
                    upper = Version(0, version.minor + 1, 0)
                else:
                    upper = Version(0, 0, version.patch + 1)
                self._comparators.append(("<", upper))
            elif op == "~":
                self._comparators.append((">=", version))
                self._comparators.append(("<", Version(version.major, version.minor + 1, 0)))
            else:
                self._comparators.append((op, version))

    def allows(self, version: Version) -> bool:
        for op, bound in self._comparators:
            if op == "==" and not version == bound:
                return False
            if op == "!=" and not version != bound:
                return False
            if op == ">=" and not version >= bound:
                return False
            if op == "<=" and not version <= bound:
                return False
            if op == ">" and not version > bound:
                return False
            if op == "<" and not version < bound:
                return False
        return True

    def __str__(self) -> str:
        return self.raw or "*"

    def __eq__(self, other) -> bool:
        return isinstance(other, Constraint) and self.raw == other.raw

    def __hash__(self):
        return hash(self.raw)
