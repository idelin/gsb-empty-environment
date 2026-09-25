"""Domain model: versioned facts, mutex rules, queries, visibility."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable, Optional


@dataclass(frozen=True)
class FactVersion:
    """One version of a fact.

    valid_from/valid_to: half-open business validity interval [from, to).
    recorded_at: transaction time at which this version was entered.
    """

    fact_id: str
    version: int
    dimension: str
    source: str
    valid_from: float
    valid_to: float
    recorded_at: float
    value: Any = None

    @property
    def ref(self) -> str:
        return f"{self.fact_id}@v{self.version}"

    def covers(self, moment: float) -> bool:
        return self.valid_from <= moment < self.valid_to

    def covers_range(self, lo: float, hi: float) -> bool:
        return self.valid_from <= lo and self.valid_to >= hi


@dataclass(frozen=True)
class MutexRule:
    """Facts from any two distinct sources in `sources` cannot co-occur."""

    rule_id: str
    version: int
    sources: frozenset

    @property
    def ref(self) -> str:
        return f"{self.rule_id}@v{self.version}"

    def forbids(self, source_a: str, source_b: str) -> bool:
        return source_a != source_b and source_a in self.sources and source_b in self.sources


@dataclass(frozen=True)
class Query:
    """observation_time: only versions recorded at or before it are visible.
    dimensions: the roles a complete selection must fill, one fact each.
    """

    query_id: str
    observation_time: float
    dimensions: tuple


def visible_versions(facts: Iterable[FactVersion], observation_time: float) -> dict:
    """Latest recorded version per fact_id among those recorded in time.

    Versions recorded after observation_time never participate in the
    historical explanation.
    """
    best: dict[str, FactVersion] = {}
    for fact in facts:
        if fact.recorded_at > observation_time:
            continue
        current = best.get(fact.fact_id)
        if current is None or (fact.recorded_at, fact.version) > (current.recorded_at, current.version):
            best[fact.fact_id] = fact
    return best


def common_interval(facts: Iterable[FactVersion]) -> Optional[tuple]:
    """Intersection of half-open business intervals, or None when empty.

    Touching intervals such as [0,5) and [5,9) have an empty intersection.
    """
    items = list(facts)
    lo = max(f.valid_from for f in items)
    hi = min(f.valid_to for f in items)
    if lo < hi:
        return (lo, hi)
    return None


def is_consistent(facts: Iterable[FactVersion], rules: Iterable[MutexRule]) -> bool:
    items = list(facts)
    for i in range(len(items)):
        for j in range(i + 1, len(items)):
            for rule in rules:
                if rule.forbids(items[i].source, items[j].source):
                    return False
    return True


def conflicting_pairs(facts: Iterable[FactVersion], rules: Iterable[MutexRule]) -> list:
    pairs = []
    items = list(facts)
    for i in range(len(items)):
        for j in range(i + 1, len(items)):
            for rule in rules:
                if rule.forbids(items[i].source, items[j].source):
                    pairs.append((items[i].source, items[j].source, rule.ref))
    return pairs
