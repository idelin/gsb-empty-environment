"""Version sets over semver values with explicit prerelease channels.

The release track and every prerelease channel are separate ordered domains.
A :class:`VersionSet` therefore stores one canonical :class:`IntervalSet` for
releases and one per *explicitly allowed* channel:

* release domain    0.0.0, 0.0.1, ... (successor bumps patch)
* channel domain    V-ch.1, V-ch.2, ... where V ranges over release values

A prerelease is contained iff its channel appears in the set *and* the
channel's interval set contains it.  Sharing the numeric triple of a release
that belongs to the set never admits a prerelease.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

from .intervals import Domain, IntervalSet, NEG, POS
from .version import Version


def _release_succ(v: Version) -> Version:
    return Version(v.major, v.minor, v.patch + 1)


RELEASE_DOMAIN = Domain("release", _release_succ)
_PRE_DOMAINS: dict[str, Domain] = {}


def pre_domain(channel: str) -> Domain:
    domain = _PRE_DOMAINS.get(channel)
    if domain is None:
        domain = Domain(f"pre:{channel}", lambda v, ch=channel: _pre_succ(v, ch))
        _PRE_DOMAINS[channel] = domain
    return domain


def _pre_succ(v: Version, channel: str) -> Version:
    return Version(v.major, v.minor, v.patch, channel, (v.seq or 1) + 1)


@dataclass(frozen=True)
class VersionSet:
    releases: IntervalSet
    channels: dict[str, IntervalSet] = field(default_factory=dict)

    @classmethod
    def empty(cls) -> "VersionSet":
        return cls(IntervalSet.empty(RELEASE_DOMAIN), {})

    @classmethod
    def release_interval(cls, lo, hi) -> "VersionSet":
        return cls(IntervalSet(RELEASE_DOMAIN, ((lo, hi),)))

    @classmethod
    def release_universe(cls) -> "VersionSet":
        return cls(IntervalSet.universe(RELEASE_DOMAIN))

    @classmethod
    def pre_universe(cls, channel: str) -> "VersionSet":
        domain = pre_domain(channel)
        return cls(
            IntervalSet.empty(RELEASE_DOMAIN),
            {channel: IntervalSet.universe(domain)},
        )

    @property
    def allowed_channels(self) -> frozenset[str]:
        return frozenset(self.channels)

    def contains(self, version: Version) -> bool:
        if version.channel is None:
            return version in self.releases
        track = self.channels.get(version.channel)
        return track is not None and version in track

    def __contains__(self, version: Version) -> bool:
        return self.contains(version)

    def __or__(self, other: "VersionSet") -> "VersionSet":
        return _binop(self, other, lambda a, b: a | b)

    def __and__(self, other: "VersionSet") -> "VersionSet":
        return _binop(self, other, lambda a, b: a & b)

    def __sub__(self, other: "VersionSet") -> "VersionSet":
        channels: dict[str, IntervalSet] = {}
        for name, track in self.channels.items():
            channels[name] = track - other.channels.get(
                name, IntervalSet.empty(pre_domain(name))
            )
        return VersionSet(self.releases - other.releases, _drop_empty(channels))

    def is_empty(self) -> bool:
        return self.releases.is_empty and all(t.is_empty for t in self.channels.values())

    def first_gap(self, anchor: Version | None = None) -> Version:
        """Smallest release not in the release track at/after ``anchor``."""
        if anchor is None:
            anchor = Version(0, 0, 0)
        if anchor.is_prerelease:
            raise ValueError("anchor for first_gap must be a release")
        return self.releases.min_absent(anchor)

    def counterexample(self, other: "VersionSet") -> Version | None:
        """Smallest version in ``self \\ other`` (global total order)."""
        best: Version | None = None
        release_diff = self.releases - other.releases
        candidate = release_diff.min_included(Version(0, 0, 0))
        if candidate is not None:
            best = candidate
        else:
            best = None
        for name, track in self.channels.items():
            diff = track - other.channels.get(name, IntervalSet.empty(pre_domain(name)))
            candidate = diff.min_included(Version(0, 0, 0, name, 1))
            if candidate is not None and (best is None or candidate < best):
                best = candidate
        return best

    def min_counterexample_against(self, other: "VersionSet") -> Version:
        witness = self.counterexample(other)
        if witness is None:
            raise ValueError("sets are equal on the release+explicit channels domain")
        return witness

    def serialize(self) -> str:
        parts = ["rel=" + self.releases.serialize()]
        for name in sorted(self.channels):
            parts.append(f"pre:{name}=" + self.channels[name].serialize())
        return ";".join(parts)

    def digest(self) -> str:
        return hashlib.sha256(self.serialize().encode("utf-8")).hexdigest()

    def __eq__(self, other: object) -> bool:
        return isinstance(other, VersionSet) and self.serialize() == other.serialize()

    def __hash__(self) -> int:
        return hash(self.serialize())

    def __str__(self) -> str:
        return self.serialize()


def _drop_empty(channels: dict[str, IntervalSet]) -> dict[str, IntervalSet]:
    return {name: track for name, track in channels.items() if not track.is_empty}


def _binop(
    a: VersionSet,
    b: VersionSet,
    op,
) -> VersionSet:
    releases = op(a.releases, b.releases)
    names = set(a.channels) | set(b.channels)
    channels: dict[str, IntervalSet] = {}
    for name in names:
        domain = pre_domain(name)
        left = a.channels.get(name, IntervalSet.empty(domain))
        right = b.channels.get(name, IntervalSet.empty(domain))
        result = op(left, right)
        if not result.is_empty:
            channels[name] = result
    return VersionSet(releases, channels)
