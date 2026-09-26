"""Brute-force verification of the derived partial order.

Enumerates every assignment of small-integer times to events that respects
the closed intervals and the strict causal edges, then computes which
precedences hold in *all* feasible assignments. The result is compared
against the symbolically derived partial order.
"""
from __future__ import annotations

from dataclasses import dataclass
from itertools import product
from typing import Iterable, Iterator, Optional

from .network import TemporalNetwork


@dataclass
class VerificationReport:
    consistent: bool
    domain: tuple
    num_assignments: int
    derived: Optional[tuple]      # sorted necessary pairs from the engine
    enumerated: Optional[tuple]   # sorted necessary pairs from enumeration

    @property
    def agrees(self) -> bool:
        return (
            self.derived is not None
            and self.enumerated is not None
            and self.derived == self.enumerated
        )

    def __str__(self) -> str:
        if not self.consistent:
            return "network is contradictory; nothing to verify"
        if self.num_assignments == 0:
            return (
                f"no feasible integer assignment in domain {self.domain}; "
                "enumeration is vacuous (try a finer/wider domain)"
            )
        verdict = "AGREE" if self.agrees else "DISAGREE"
        return (
            f"{self.num_assignments} feasible assignments over {self.domain}; "
            f"derived={list(self.derived)} enumerated={list(self.enumerated)} -> {verdict}"
        )


def feasible_assignments(net: TemporalNetwork, domain: Iterable[int]) -> Iterator[dict]:
    """Yield every integer assignment satisfying intervals and causal edges."""
    events = net.events()
    intervals = net.intervals()
    missing = [e for e in events if e not in intervals]
    if missing:
        raise ValueError(f"events without intervals cannot be enumerated: {missing}")
    dom = list(domain)
    choices = [
        [t for t in dom if intervals[e].lo <= t <= intervals[e].hi] for e in events
    ]
    causal = net.causal_edges()
    for combo in product(*choices):
        assign = dict(zip(events, combo))
        if all(assign[c.cause] < assign[c.effect] for c in causal):
            yield assign


def necessary_by_enumeration(net: TemporalNetwork, domain: Iterable[int]) -> tuple:
    """Pairs (a, b) with t(a) < t(b) in every feasible integer assignment.

    Returns (pairs_or_None, assignment_count); pairs is None when the
    enumeration is vacuous (no feasible assignment in the domain).
    """
    events = net.events()
    pairs = None
    count = 0
    for assign in feasible_assignments(net, domain):
        count += 1
        cur = frozenset(
            (a, b) for a in events for b in events if a != b and assign[a] < assign[b]
        )
        pairs = cur if pairs is None else pairs & cur
    return pairs, count


def verify(net: TemporalNetwork, domain: Iterable[int]) -> VerificationReport:
    """Cross-check the derived partial order against brute-force enumeration."""
    dom = tuple(domain)
    if not net.is_consistent():
        return VerificationReport(False, dom, 0, None, None)
    derived = tuple(sorted(net.necessary_pairs()))
    enum_pairs, count = necessary_by_enumeration(net, dom)
    enumerated = None if enum_pairs is None else tuple(sorted(enum_pairs))
    return VerificationReport(True, dom, count, derived, enumerated)
