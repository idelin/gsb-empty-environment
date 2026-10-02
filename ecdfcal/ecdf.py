"""Empirical CDF for discrete samples.

Ties jump *simultaneously*: the step at a repeated value carries the summed
weight of every observation identity equal to that value.  Multiplicities
(observation identity counts) are kept explicitly and separately from
probability masses (weights), so weighted and unweighted readings are both
available.
"""
from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class ECDF:
    """Right-continuous empirical CDF over a discrete support.

    Attributes:
        support: sorted distinct observed values.
        multiplicities: number of observation identities at each support point.
        masses: Fraction probability mass at each support point.
        cumulative: Fraction cumulative probability at each support point.
        n_observations: total number of observation identities.
        total_weight: sum of observation weights (== n_observations if unweighted).
    """

    support: tuple
    multiplicities: tuple
    masses: tuple
    cumulative: tuple
    n_observations: int
    total_weight: Fraction

    @classmethod
    def from_samples(cls, samples, weights=None) -> "ECDF":
        values = list(samples)
        if not values:
            raise ValueError("empty sample is not allowed")
        if weights is None:
            obs_weights = [Fraction(1)] * len(values)
        else:
            obs_weights = [Fraction(w) for w in weights]
            if len(obs_weights) != len(values):
                raise ValueError("weights must match samples in length")
            if any(w <= 0 for w in obs_weights):
                raise ValueError("weights must be positive")
        # Canonical order: sort by value.  Duplicate values keep their identity
        # through the multiplicity counter; input order never matters.
        order = sorted(range(len(values)), key=lambda i: values[i])
        sorted_values = [values[i] for i in order]
        sorted_weights = [obs_weights[i] for i in order]
        support, multiplicities, masses = [], [], []
        total = Fraction(0)
        for value, weight in zip(sorted_values, sorted_weights):
            if support and value == support[-1]:
                multiplicities[-1] += 1
                masses[-1] += weight
            else:
                support.append(value)
                multiplicities.append(1)
                masses.append(weight)
            total += weight
        masses = [m / total for m in masses]
        cumulative = []
        running = Fraction(0)
        for mass in masses:
            running += mass
            cumulative.append(running)
        return cls(
            tuple(support),
            tuple(multiplicities),
            tuple(masses),
            tuple(cumulative),
            len(values),
            total,
        )

    def cdf_at(self, x) -> Fraction:
        """F(x) = P(X <= x); right-continuous, jumps by full multiplicity."""
        idx = bisect_right(self.support, x)
        if idx == 0:
            return Fraction(0)
        return self.cumulative[idx - 1]
