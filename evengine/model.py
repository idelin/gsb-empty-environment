"""Core data model: facts (evidence), rules and requirements.

Every fact, rule and step carries a stable ID and a version so results are
reproducible and evidence is replayable.  Time is integral; intervals are
half-open [start, end), so exactly touching boundaries leave no gap.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Tuple


@dataclass(frozen=True)
class Evidence:
    """A single piece of evidence (a fact).

    Covers the half-open interval [start, end).  ``expires_at`` (optional)
    makes the evidence ineligible at any t >= expires_at: expired evidence
    cannot fill a requirement.
    """

    evidence_id: str
    version: int
    source_id: str
    category: str
    start: int
    end: int
    expires_at: Optional[int] = None

    def active_at(self, t: int) -> bool:
        return self.start <= t < self.end and (
            self.expires_at is None or t < self.expires_at
        )


@dataclass(frozen=True)
class CategoryRequirement:
    """"At least ``min_sources`` independent sources in ``category``."""

    category: str
    min_sources: int


@dataclass(frozen=True)
class Rule:
    """A conclusion rule: joint category requirements.

    When ``exclusive_sources`` is True, one source may count toward at most
    one category at a time (cross-category source exclusivity); satisfaction
    is then a joint constraint, not a per-category count.
    """

    rule_id: str
    version: int
    requirements: Tuple[CategoryRequirement, ...]
    exclusive_sources: bool = True
