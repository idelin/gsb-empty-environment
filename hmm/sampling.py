"""Sampling with caller-injected randomness.

Every function takes an explicit ``random.Random``-compatible instance;
nothing here touches global randomness.  Results are tagged
``kind="empirical"`` and carry the seed (as reported by the caller) and the
sample size, keeping them clearly distinct from exact inference results
(``kind="exact"``).
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .model import HMM, MISSING


@dataclass(frozen=True)
class SampledSequences:
    kind: str  # always "empirical"
    seed: object  # seed reported by the caller (metadata only)
    n_samples: int
    states: tuple
    observations: tuple


@dataclass(frozen=True)
class EmpiricalPosterior:
    kind: str  # always "empirical"
    seed: object
    n_samples: int
    time: int
    estimates: Optional[tuple]  # None when no sample matched the observations
    n_accepted: int


def _draw(rng, weights) -> int:
    r = rng.random()
    acc = 0.0
    last = len(weights) - 1
    for idx, w in enumerate(weights):
        acc += w
        if r <= acc:
            return idx
    return last


def sample_sequence(model: HMM, length: int, rng):
    """Sample one ``(states, observations)`` pair of the given length."""
    if length < 0:
        raise ValueError("length must be >= 0")
    states = []
    obs = []
    if length == 0:
        return (), ()
    s = _draw(rng, model.initial)
    for t in range(length):
        states.append(s)
        dist = model.emission[s]
        symbols = list(dist.keys())
        obs.append(symbols[_draw(rng, [dist[k] for k in symbols])])
        if t + 1 < length:
            s = _draw(rng, model.transition[s])
    return tuple(states), tuple(obs)


def sample_sequences(model: HMM, length: int, n: int, rng, seed=None) -> SampledSequences:
    """Sample ``n`` sequences; records the caller-reported seed and ``n``."""
    if n < 1:
        raise ValueError("n must be >= 1")
    states, observations = [], []
    for _ in range(n):
        st, ob = sample_sequence(model, length, rng)
        states.append(st)
        observations.append(ob)
    return SampledSequences(
        kind="empirical",
        seed=seed,
        n_samples=n,
        states=tuple(states),
        observations=tuple(observations),
    )


def estimate_state_posterior(model: HMM, observations, time: int, n: int, rng, seed=None) -> EmpiricalPosterior:
    """Rejection-sampling estimate of ``P(S_time = . | observations)``.

    ``MISSING`` entries in ``observations`` match anything.  This is an
    empirical estimate (``kind="empirical"``), not an exact posterior.
    """
    obs_query = list(observations)
    T = len(obs_query)
    if not 0 <= time < T:
        raise IndexError(f"time {time} out of range for T={T}")
    counts = [0] * model.n_states
    accepted = 0
    for _ in range(n):
        states, obs = sample_sequence(model, T, rng)
        if all(q is MISSING or q == o for q, o in zip(obs_query, obs)):
            accepted += 1
            counts[states[time]] += 1
    estimates = None if accepted == 0 else tuple(c / accepted for c in counts)
    return EmpiricalPosterior(
        kind="empirical",
        seed=seed,
        n_samples=n,
        time=time,
        estimates=estimates,
        n_accepted=accepted,
    )
