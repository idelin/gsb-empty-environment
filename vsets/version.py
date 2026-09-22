"""Semantic versions: major.minor.patch with optional prerelease channels.

A prerelease is written ``2.0.0-alpha.1`` (sequence defaults to 1 when the
sequence is omitted).  Prereleases of different channels are never ordered
against each other except through the total ordering defined here, which is
only used for display and for selecting the smallest counterexample.
"""
from __future__ import annotations

import re
from dataclasses import dataclass

_VERSION_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z][A-Za-z0-9]*)(?:\.(\d+))?)?$")


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    channel: str | None = None
    seq: int | None = None

    @classmethod
    def parse(cls, text: str) -> "Version":
        m = _VERSION_RE.match(text.strip())
        if not m:
            raise ValueError(f"invalid version: {text!r}")
        major, minor, patch, channel, seq = m.groups()
        return cls(
            int(major),
            int(minor),
            int(patch),
            channel,
            int(seq) if seq is not None else (1 if channel else None),
        )

    @property
    def is_prerelease(self) -> bool:
        return self.channel is not None

    @property
    def base(self) -> "Version":
        return Version(self.major, self.minor, self.patch)

    def as_prerelease(self, channel: str, seq: int = 1) -> "Version":
        return Version(self.major, self.minor, self.patch, channel, seq)

    def __str__(self) -> str:
        core = f"{self.major}.{self.minor}.{self.patch}"
        if self.channel is None:
            return core
        return f"{core}-{self.channel}.{self.seq}"

    def _sort_key(self):
        if self.channel is None:
            return (self.major, self.minor, self.patch, 1, "", 0)
        return (self.major, self.minor, self.patch, 0, self.channel, self.seq or 1)

    def __lt__(self, other: "Version") -> bool:
        return self._sort_key() < other._sort_key()

    def __le__(self, other: "Version") -> bool:
        return self._sort_key() <= other._sort_key()

    def __gt__(self, other: "Version") -> bool:
        return self._sort_key() > other._sort_key()

    def __ge__(self, other: "Version") -> bool:
        return self._sort_key() >= other._sort_key()
