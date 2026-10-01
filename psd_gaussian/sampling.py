"""Correlated Gaussian sampling from a spectral decomposition.

The random source is *injected by the caller* (any object exposing
``gauss(0.0, 1.0)``, e.g. ``random.Random(seed)``).  The seed and sample
size are recorded in a :class:`SamplingRecord`; re-injecting an equivalent
stream (same seed, or a restored ``getstate`` snapshot) reproduces the exact
same samples.
"""
from __future__ import annotations

from dataclasses import dataclass

from .decomposition import Decomposition


@dataclass(frozen=True)
class SamplingRecord:
    """Provenance of a sampling run: caller-supplied seed and sample size."""

    seed: int | None
    n_samples: int
    dim: int
    kind: str = "empirical"


def sample_correlated(
    decomp: Decomposition,
    mean,
    n_samples: int,
    rng,
    *,
    seed: int | None = None,
) -> tuple[list[list[float]], SamplingRecord]:
    """Draw ``n_samples`` from ``N(mean, A A^T)`` where ``A = decomp.factor``.

    ``rng`` is the caller-injected random source.  Zero-variance directions
    contribute exactly ``0 * z == 0``, so those coordinates equal the mean
    exactly; rank-1 factors place every sample on the corresponding line.
    """
    if n_samples < 0:
        raise ValueError("n_samples must be non-negative")
    mu = [float(x) for x in mean]
    factor = decomp.factor
    dim = len(mu)
    k = len(factor[0]) if factor and factor[0] else 0
    samples = []
    for _ in range(n_samples):
        z = [rng.gauss(0.0, 1.0) for _ in range(k)]
        samples.append(
            [mu[i] + sum(factor[i][j] * z[j] for j in range(k)) for i in range(dim)]
        )
    return samples, SamplingRecord(seed=seed, n_samples=n_samples, dim=dim)
