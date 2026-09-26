"""Versioned maps: local revisions with provenance evidence.

Every revision creates a new immutable version; old versions remain fully
queryable, so adding (for example) a new pause never changes the answers
given by an older version.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .mapping import Evidence, PiecewiseMap
from .types import exact


@dataclass(frozen=True)
class Revision:
    version: int
    base_version: int
    start: Fraction
    end: Fraction
    reason: str
    evidence: tuple[Evidence, ...]


class MapHistory:
    def __init__(self, base: PiecewiseMap):
        self._versions: dict[int, PiecewiseMap] = {1: base}
        self._revisions: list[Revision] = []

    def version(self, vid: int) -> PiecewiseMap:
        return self._versions[vid]

    @property
    def latest(self) -> int:
        return max(self._versions)

    @property
    def revisions(self) -> tuple[Revision, ...]:
        return tuple(self._revisions)

    def revise(self, base_version, start, end, spec, reason="", evidence=()) -> int:
        """Apply a local revision on top of `base_version`; return the new id."""
        base = self._versions[base_version]
        new_map = base.replace_range(start, end, spec, evidence)
        vid = self.latest + 1
        self._versions[vid] = new_map
        self._revisions.append(Revision(
            vid, base_version, exact(start), exact(end), reason, tuple(evidence),
        ))
        return vid

    def evidence_at(self, vid, t) -> tuple[Evidence, ...]:
        """Provenance of the segment covering real time `t` in version `vid`."""
        return self._versions[vid].segment_at(t).evidence
