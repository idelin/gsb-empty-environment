"""Endpoint-sweep evaluation engine.

Satisfaction is evaluated jointly across categories: with exclusive
sources, a max-flow check decides whether the available independent
sources can be assigned to categories so every category gets its
required k distinct sources.  Per-category counting alone is not enough.

The sweep collects every interval boundary and expiry instant, evaluates
each atomic segment once, and merges adjacent satisfied segments, so
retracting the last supporter shrinks intervals exactly at the true
boundary and touching boundaries never produce zero-length gaps.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Dict, Optional, Sequence, Set, Tuple

from .model import Evidence, Rule

Interval = Tuple[int, int]


def time_points(evidence: Sequence[Evidence], domain: Optional[Interval] = None):
    """All instants where the active set can change, plus domain bounds."""
    points = set()
    for item in evidence:
        points.add(item.start)
        points.add(item.end)
        if item.expires_at is not None:
            points.add(item.expires_at)
    if domain is not None:
        points.add(domain[0])
        points.add(domain[1])
    return sorted(points)


def sources_by_category(
    evidence: Sequence[Evidence], t: int
) -> Dict[str, Set[str]]:
    """Distinct active sources per category at instant t.

    Duplicate evidence from the same source collapses to one entry, and
    expired evidence is excluded by ``Evidence.active_at``.
    """
    pools: Dict[str, Set[str]] = {}
    for item in evidence:
        if item.active_at(t):
            pools.setdefault(item.category, set()).add(item.source_id)
    return pools


def _joint_flow(pools: Dict[str, Set[str]], rule: Rule) -> int:
    """Max source->category assignment under source exclusivity.

    Categories demand ``min_sources`` units; each source supplies at most
    one unit total, so a source can support only one category.
    """
    capacity: Dict[object, Dict[object, int]] = {}

    def add(u, v, c):
        capacity.setdefault(u, {})
        capacity[u][v] = capacity[u].get(v, 0) + c
        capacity.setdefault(v, {})
        capacity[v].setdefault(u, 0)

    all_sources: Set[str] = set()
    for index, req in enumerate(rule.requirements):
        add("S", ("cat", index), req.min_sources)
        for source in pools.get(req.category, ()):
            all_sources.add(source)
            add(("cat", index), ("src", source), 1)
    for source in all_sources:
        add(("src", source), "T", 1)

    flow = 0
    while True:
        prev = {"S": None}
        queue = deque(["S"])
        while queue and "T" not in prev:
            u = queue.popleft()
            for v, residual in capacity.get(u, {}).items():
                if residual > 0 and v not in prev:
                    prev[v] = u
                    queue.append(v)
        if "T" not in prev:
            return flow
        bottleneck = None
        v = "T"
        while prev[v] is not None:
            u = prev[v]
            residual = capacity[u][v]
            bottleneck = residual if bottleneck is None else min(bottleneck, residual)
            v = u
        v = "T"
        while prev[v] is not None:
            u = prev[v]
            capacity[u][v] -= bottleneck
            capacity[v][u] += bottleneck
            v = u
        flow += bottleneck


def satisfied_at(rule: Rule, evidence: Sequence[Evidence], t: int) -> bool:
    """Joint satisfaction of all category requirements at instant t."""
    pools = sources_by_category(evidence, t)
    if not rule.exclusive_sources:
        return all(
            len(pools.get(req.category, ())) >= req.min_sources
            for req in rule.requirements
        )
    needed = sum(req.min_sources for req in rule.requirements)
    return _joint_flow(pools, rule) >= needed


def _merge(intervals):
    """Union of half-open intervals; touching boundaries merge (no gap)."""
    merged = []
    for start, end in sorted(intervals):
        if start >= end:
            continue
        if merged and start <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], end))
        else:
            merged.append((start, end))
    return merged


def _satisfied_segments(rule, evidence, domain):
    points = time_points(evidence, domain)
    segments = []
    for a, b in zip(points, points[1:]):
        if a >= b:
            continue
        if domain is not None and (b <= domain[0] or a >= domain[1]):
            continue
        if satisfied_at(rule, evidence, a):
            segments.append((a, b))
    return segments


@dataclass(frozen=True)
class Evaluation:
    rule_id: str
    rule_version: int
    domain: Interval
    satisfied: Tuple[Interval, ...]
    gaps: Tuple[Interval, ...]
    longest: Optional[Interval]

    def as_dict(self):
        return {
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "domain": list(self.domain),
            "satisfied": [list(iv) for iv in self.satisfied],
            "gaps": [list(iv) for iv in self.gaps],
            "longest": list(self.longest) if self.longest else None,
        }


def evaluate(
    rule: Rule, evidence: Sequence[Evidence], domain: Optional[Interval] = None
) -> Evaluation:
    """Satisfied intervals, gaps and the longest satisfied interval."""
    if domain is None:
        points = time_points(evidence)
        if not points:
            domain = (0, 0)
        else:
            domain = (points[0], points[-1])
    satisfied = tuple(_merge(_satisfied_segments(rule, evidence, domain)))

    gaps = []
    cursor = domain[0]
    for start, end in satisfied:
        if start > cursor:
            gaps.append((cursor, start))
        cursor = max(cursor, end)
    if cursor < domain[1]:
        gaps.append((cursor, domain[1]))
    gaps = tuple(g for g in gaps if g[1] > g[0])

    longest = None
    for interval in satisfied:
        if longest is None or (interval[1] - interval[0]) > (longest[1] - longest[0]):
            longest = interval
    return Evaluation(rule.rule_id, rule.version, domain, satisfied, gaps, longest)
