"""Exact and empirical analysis helpers.

Naming convention: functions returning ``Fraction`` objects are *exact
conclusions*; functions returning floats are *empirical estimates*.
"""

from __future__ import annotations

from fractions import Fraction

from .exact_alias import AliasTable


def enumerate_exact_distribution(table: AliasTable, max_outcomes: int = 2_000_000):
    """[EXACT] Enumerate every (column, threshold-draw) outcome.

    Column ``j`` is chosen with probability ``1/n``; the threshold draw is
    uniform over ``range(q_j)`` where ``q_j`` is the denominator of the
    column threshold.  Every ``(j, v)`` pair therefore has exact probability
    ``1 / (n * q_j)``.  Summing over all pairs yields the exact sampling
    distribution of the table, suitable for small weight sets as a proof
    that the table introduces no bias.
    """
    n = table.n
    total_outcomes = sum(p.denominator for p in table.prob)
    if total_outcomes > max_outcomes:
        raise ValueError(
            f"outcome space too large to enumerate: "
            f"{total_outcomes} > {max_outcomes}"
        )
    dist = [Fraction(0)] * n
    for j in range(n):
        threshold = table.prob[j]
        q, p = threshold.denominator, threshold.numerator
        outcome_prob = Fraction(1, n * q)
        for v in range(q):
            item = j if v < p else table.alias[j]
            dist[item] += outcome_prob
    return dist


def empirical_frequencies(table: AliasTable, rng, draws: int):
    """[EMPIRICAL] Observed frequencies over `draws` samples (floats)."""
    counts = [0] * table.n
    for _ in range(draws):
        counts[table.sample(rng)] += 1
    return [c / draws for c in counts], counts
