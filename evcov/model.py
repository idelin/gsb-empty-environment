"""Versioned domain facts and rules. All entities carry stable IDs."""

from __future__ import annotations

from dataclasses import dataclass, replace


@dataclass(frozen=True)
class Source:
    """An independent origin of evidence (e.g. a sensor, an auditor)."""

    id: str
    version: int = 1


@dataclass(frozen=True)
class EvidenceItem:
    """A piece of evidence, active during [valid_from, valid_until).

    Half-open interval: an item expiring at t no longer counts at t.
    Retraction bumps the version so history stays replayable.
    """

    id: str
    source_id: str
    categories: frozenset[str]
    valid_from: int
    valid_until: int
    version: int = 1
    retracted: bool = False

    def retract(self) -> "EvidenceItem":
        return replace(self, retracted=True, version=self.version + 1)


@dataclass(frozen=True)
class CategoryRequirement:
    """A category needing at least `min_sources` independent sources."""

    category: str
    min_sources: int


@dataclass(frozen=True)
class Rule:
    """A conclusion: category requirements over a required time horizon."""

    id: str
    version: int
    requirements: tuple[CategoryRequirement, ...]
    horizon_start: int
    horizon_end: int
