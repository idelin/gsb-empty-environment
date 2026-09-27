"""Synthetic injectable mechanisms on the counting query.

Datasets are tuples of synthetic 0/1 records. Adjacency is add-one
(injection): D' = D + (injected_record,). Only synthetic data is ever
processed by this package.

Three mechanisms are provided:
  - laplace_count:        valid epsilon-DP under add-one adjacency.
  - leaky_laplace_count:  broken; leaks the exact count with some
                          probability, violating its epsilon claim.
  - deterministic_count:  broken; no noise at all (also demonstrates
                          zero-observation handling in the auditor).
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from typing import Callable, Sequence


@dataclass
class Mechanism:
    """A randomized mechanism: dataset + rng -> output (a float here)."""

    name: str
    params: dict
    sample: Callable[[Sequence[int], random.Random], float]

    def __call__(self, dataset: Sequence[int], rng: random.Random) -> float:
        return self.sample(dataset, rng)


def sample_laplace(rng: random.Random, scale: float) -> float:
    """Draw from Laplace(0, scale) by inversion."""
    u = rng.random() - 0.5
    return -scale * math.copysign(1.0, u) * math.log1p(-2.0 * abs(u))


def laplace_count(epsilon: float) -> Mechanism:
    """count(D) + Laplace(1/epsilon); epsilon-DP under add-one adjacency."""
    if epsilon <= 0:
        raise ValueError("epsilon must be positive")

    def sample(dataset: Sequence[int], rng: random.Random) -> float:
        return float(sum(dataset)) + sample_laplace(rng, 1.0 / epsilon)

    return Mechanism(name="laplace_count", params={"epsilon": epsilon}, sample=sample)


def leaky_laplace_count(epsilon: float, leak_probability: float) -> Mechanism:
    """Broken mechanism: with probability leak_probability, output the exact count.

    Claims epsilon-DP but violates it for any leak_probability > 0; used to
    demonstrate that the auditor flags clearly violating mechanisms with a
    reproducible event.
    """
    if epsilon <= 0:
        raise ValueError("epsilon must be positive")
    if not 0.0 <= leak_probability <= 1.0:
        raise ValueError("leak_probability must be in [0, 1]")

    def sample(dataset: Sequence[int], rng: random.Random) -> float:
        count = float(sum(dataset))
        if rng.random() < leak_probability:
            return count
        return count + sample_laplace(rng, 1.0 / epsilon)

    return Mechanism(
        name="leaky_laplace_count",
        params={"epsilon": epsilon, "leak_probability": leak_probability},
        sample=sample,
    )


def deterministic_count() -> Mechanism:
    """Exact count with no noise; not differentially private for any epsilon."""

    def sample(dataset: Sequence[int], rng: random.Random) -> float:
        return float(sum(dataset))

    return Mechanism(name="deterministic_count", params={}, sample=sample)
