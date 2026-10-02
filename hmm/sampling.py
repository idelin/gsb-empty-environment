"""Sampling from an HMM with a caller-injected random source.

The caller supplies a ``random.Random`` instance (or a seed used to build
one), so reproducibility is owned by the caller.  Every empirical result
carries the seed and the sample size, and is labelled ``kind="empirical"``
to distinguish it from exact (dynamic-programming / enumeration) results.
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field


def _draw_index(probs, rng: random.Random) -> int:
    r = rng.random()
    acc = 0.0
    for i, p in enumerate(probs):
        acc += p
        if r < acc:
            return i
    return len(probs) - 1  # guard against float round-off at the boundary


def _draw_symbol(emission_row: dict, rng: random.Random):
    r = rng.random()
    acc = 0.0
    last = None
    for symbol, p in emission_row.items():
        acc += p
        last = symbol
        if r < acc:
            return symbol
    return last


def sample_path(model, length: int, rng: random.Random):
    """Sample one (states, observations) pair.

    ``rng`` is injected by the caller; this function never touches global
    randomness.
    """
    if length < 0:
        raise ValueError("length must be >= 0")
    states, observations = [], []
    if length == 0:
        return states, observations
    s = _draw_index(model.initial, rng)
    states.append(s)
    observations.append(_draw_symbol(model.emission[s], rng))
    for t in range(1, length):
        A = model.transition_matrix(t - 1)
        s = _draw_index(A[states[-1]], rng)
        states.append(s)
        observations.append(_draw_symbol(model.emission[s], rng))
    return states, observations


@dataclass
class EmpiricalEstimate:
    """A Monte-Carlo estimate; always labelled empirical, with provenance."""

    kind: str
    seed: object
    n_samples: int
    length: int
    state_frequencies: list[list[float]] = field(default_factory=list)

    def __post_init__(self):
        if self.kind != "empirical":
            raise ValueError("EmpiricalEstimate must have kind='empirical'")


def estimate_state_frequencies(model, length: int, n_samples: int, seed) -> EmpiricalEstimate:
    """Empirical per-time state frequencies from ``n_samples`` trajectories.

    The seed is recorded alongside the sample size so the estimate is
    reproducible and clearly separated from exact posteriors.
    """
    if n_samples < 1:
        raise ValueError("n_samples must be >= 1")
    rng = random.Random(seed)
    n = model.n_states
    counts = [[0] * n for _ in range(length)]
    for _ in range(n_samples):
        states, _ = sample_path(model, length, rng)
        for t, s in enumerate(states):
            counts[t][s] += 1
    freqs = [[c / n_samples for c in row] for row in counts]
    return EmpiricalEstimate("empirical", seed, n_samples, length, freqs)
