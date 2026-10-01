"""Synthetic-data simulation and exact small-domain reference distributions.

Only synthetic data is handled here: true values are drawn from a
user-specified distribution and privatized through known channels.  No real
user data is involved, and none of these experiments constitutes a formal
privacy proof.
"""
from __future__ import annotations

import math
import random
from dataclasses import dataclass

from .channels import RandomizedResponseChannel, validate_distribution


def simulate_counts(
    channel: RandomizedResponseChannel,
    true_dist: list[float],
    n: int,
    rng: random.Random | None = None,
) -> list[int]:
    """Draw n synthetic participants and return the observed output counts."""
    validate_distribution(true_dist, channel.k)
    if n <= 0:
        raise ValueError(f"n must be positive, got {n}")
    rng = rng or random.Random()
    k = channel.k
    cum = []
    acc = 0.0
    for x in true_dist:
        acc += x
        cum.append(acc)
    counts = [0] * k
    for _ in range(n):
        u = rng.random()
        true_value = next(i for i, c in enumerate(cum) if u < c)
        counts[channel.sample(true_value, rng)] += 1
    return counts


def theoretical_output_distribution(
    channel: RandomizedResponseChannel, true_dist: list[float]
) -> list[float]:
    """Theoretical output probabilities A x (what simulated frequencies approach)."""
    return channel.output_distribution(true_dist)


@dataclass
class ExactBinaryReference:
    """Exact sampling distribution of the binary-RR raw estimator.

    For k = 2 with p = p_stay and true P(X = 0) = x0, the count of reported
    zeros is exactly Binomial(n, s) with s = (2p - 1) x0 + (1 - p), so the
    raw inverse estimate x0_hat = (f0 - (1 - p)) / (2p - 1) has an exactly
    enumerable distribution.  This serves as a ground-truth reference for
    bias checks on small domains.
    """

    epsilon: float
    x0: float
    n: int
    support: list[float]          # possible raw estimates
    probabilities: list[float]    # exact probability of each estimate
    mean_raw: float               # exact E[raw estimate]  (= x0: unbiased)
    var_raw: float                # exact Var(raw estimate)
    bias_clipped: float           # exact E[clip to [0,1]] - x0

    def mean_of(self, values: list[float]) -> float:
        return sum(p * v for p, v in zip(self.probabilities, values))


def exact_binary_reference(epsilon: float, x0: float, n: int) -> ExactBinaryReference:
    """Enumerate the exact distribution of the binary raw/clipped estimator."""
    channel = RandomizedResponseChannel(k=2, epsilon=epsilon)
    if channel.is_degenerate:
        raise ValueError("epsilon = 0: output independent of input; estimator not identifiable")
    if not 0.0 <= x0 <= 1.0:
        raise ValueError(f"x0 must be in [0, 1], got {x0}")
    if n <= 0:
        raise ValueError(f"n must be positive, got {n}")
    p = channel.p_stay
    s = (2.0 * p - 1.0) * x0 + (1.0 - p)
    support, probs, clipped_vals = [], [], []
    for c in range(n + 1):
        pmf = math.comb(n, c) * s**c * (1.0 - s) ** (n - c)
        raw = (c / n - (1.0 - p)) / (2.0 * p - 1.0)
        support.append(raw)
        probs.append(pmf)
        clipped_vals.append(min(max(raw, 0.0), 1.0))
    mean_raw = sum(pr * v for pr, v in zip(probs, support))
    var_raw = sum(pr * (v - mean_raw) ** 2 for pr, v in zip(probs, support))
    mean_clipped = sum(pr * v for pr, v in zip(probs, clipped_vals))
    return ExactBinaryReference(
        epsilon=epsilon,
        x0=x0,
        n=n,
        support=support,
        probabilities=probs,
        mean_raw=mean_raw,
        var_raw=var_raw,
        bias_clipped=mean_clipped - x0,
    )
