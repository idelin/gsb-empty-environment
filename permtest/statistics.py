"""Discrete two-sample statistics: frequencies, ECDF, total variation, KS gap.

Conventions
-----------
- Every element of a sample is a distinct observation identity, even when
  values repeat. Repeated values contribute their full multiplicity.
- Optional per-observation weights scale each identity's contribution;
  weights must be non-negative and have positive total per sample.
- The empirical CDF is right-continuous and jumps *simultaneously* by the
  full multiplicity (or total weight) of all observations sharing a value.
- All statistics depend only on the (weighted) multiset of values, so
  reordering the input sequences never changes the result.
"""
from __future__ import annotations

from collections import Counter
from typing import Hashable, Sequence


def _validate(sample: Sequence, weights: Sequence[float] | None) -> None:
    if len(sample) == 0:
        raise ValueError("empty sample is not allowed")
    if weights is None:
        return
    if len(weights) != len(sample):
        raise ValueError("weights must match sample length")
    if any(w < 0 for w in weights):
        raise ValueError("weights must be non-negative")
    if sum(weights) <= 0:
        raise ValueError("weights must have positive total")


def frequencies(
    sample: Sequence[Hashable], weights: Sequence[float] | None = None
) -> Counter:
    """Return value -> total weight (default weight 1 per observation).

    Repeated values accumulate their full multiplicity; each observation
    keeps its own identity and contributes its own weight.
    """
    _validate(sample, weights)
    counts: Counter = Counter()
    if weights is None:
        counts.update(sample)
    else:
        for value, w in zip(sample, weights):
            counts[value] += w
    return counts


def ecdf_points(
    sample: Sequence, weights: Sequence[float] | None = None
) -> list[tuple]:
    """Return sorted (value, cumulative proportion) points after each jump.

    All observations sharing a value jump together (simultaneous jump by
    full multiplicity / total weight).
    """
    counts = frequencies(sample, weights)
    total = float(sum(counts.values()))
    points = []
    cumulative = 0.0
    for value in sorted(counts):
        cumulative += counts[value] / total
        points.append((value, cumulative))
    return points


def total_variation(
    sample_a: Sequence,
    sample_b: Sequence,
    weights_a: Sequence[float] | None = None,
    weights_b: Sequence[float] | None = None,
) -> float:
    """Total variation distance between the two empirical distributions.

    TV = 0.5 * sum_x |p_a(x) - p_b(x)| over the union of supports.
    """
    counts_a = frequencies(sample_a, weights_a)
    counts_b = frequencies(sample_b, weights_b)
    total_a = float(sum(counts_a.values()))
    total_b = float(sum(counts_b.values()))
    distance = 0.0
    for value in set(counts_a) | set(counts_b):
        distance += abs(
            counts_a.get(value, 0.0) / total_a - counts_b.get(value, 0.0) / total_b
        )
    return 0.5 * distance


def ks_statistic(
    sample_a: Sequence,
    sample_b: Sequence,
    weights_a: Sequence[float] | None = None,
    weights_b: Sequence[float] | None = None,
) -> float:
    """Maximum absolute difference between the two empirical CDFs.

    Both ECDFs jump simultaneously at each distinct pooled value, by the
    full multiplicity (or total weight) at that value.
    """
    counts_a = frequencies(sample_a, weights_a)
    counts_b = frequencies(sample_b, weights_b)
    total_a = float(sum(counts_a.values()))
    total_b = float(sum(counts_b.values()))
    cum_a = 0.0
    cum_b = 0.0
    gap = 0.0
    for value in sorted(set(counts_a) | set(counts_b)):
        cum_a += counts_a.get(value, 0.0) / total_a
        cum_b += counts_b.get(value, 0.0) / total_b
        diff = abs(cum_a - cum_b)
        if diff > gap:
            gap = diff
    return gap
