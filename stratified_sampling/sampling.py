"""Drawing stratified and two-stage cluster samples with an injected RNG.

The random source is always supplied by the caller (dependency
injection); the seed and the realised sample sizes are recorded in a
SamplingRecord so results are reproducible and auditable.
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from typing import Mapping, Sequence

from .cluster import ClusterSample, ClusterStratumSample
from .errors import StratifiedSamplingError
from .stratified import StratumSample


@dataclass(frozen=True)
class SamplingRecord:
    """Audit trail: which seed and which sample sizes produced a sample."""

    seed: int | None
    stage1_sizes: tuple[tuple[str, int], ...]
    stage2_sizes: tuple[tuple[str, tuple[int, ...]], ...] | None


def draw_stratified_sample(
    population: Mapping[str, Sequence[float]],
    sample_sizes: Mapping[str, int],
    *,
    rng: random.Random,
    seed: int | None = None,
) -> tuple[list[StratumSample], SamplingRecord]:
    """Draw SRSWOR within each stratum. The caller injects the RNG."""
    samples: list[StratumSample] = []
    sizes: list[tuple[str, int]] = []
    for name, values in population.items():
        n_h = sample_sizes.get(name)
        if n_h is None:
            raise StratifiedSamplingError(
                f"no sample size given for stratum {name!r}"
            )
        if not 1 <= n_h <= len(values):
            raise StratifiedSamplingError(
                f"stratum {name!r}: invalid sample size {n_h}"
            )
        drawn = rng.sample(list(values), n_h)
        samples.append(StratumSample(name, len(values), drawn))
        sizes.append((name, n_h))
    record = SamplingRecord(seed=seed, stage1_sizes=tuple(sizes), stage2_sizes=None)
    return samples, record


def draw_two_stage_sample(
    population: Mapping[str, Sequence[Sequence[float]]],
    first_stage_sizes: Mapping[str, int],
    second_stage_sizes: Mapping[str, int],
    *,
    rng: random.Random,
    seed: int | None = None,
) -> tuple[list[ClusterStratumSample], SamplingRecord]:
    """Draw clusters then elements (SRSWOR at both stages)."""
    samples: list[ClusterStratumSample] = []
    stage1: list[tuple[str, int]] = []
    stage2: list[tuple[str, tuple[int, ...]]] = []
    for name, clusters in population.items():
        m_h = first_stage_sizes.get(name)
        n_h = second_stage_sizes.get(name)
        if m_h is None or n_h is None:
            raise StratifiedSamplingError(
                f"missing stage sizes for stratum {name!r}"
            )
        if not 1 <= m_h <= len(clusters):
            raise StratifiedSamplingError(
                f"stratum {name!r}: invalid first-stage size {m_h}"
            )
        drawn_clusters = rng.sample(list(clusters), m_h)
        cluster_samples: list[ClusterSample] = []
        for cluster in drawn_clusters:
            if not 1 <= n_h <= len(cluster):
                raise StratifiedSamplingError(
                    f"stratum {name!r}: invalid second-stage size {n_h}"
                )
            cluster_samples.append(
                ClusterSample(len(cluster), rng.sample(list(cluster), n_h))
            )
        samples.append(ClusterStratumSample(name, len(clusters), cluster_samples))
        stage1.append((name, m_h))
        stage2.append((name, tuple([n_h] * m_h)))
    record = SamplingRecord(
        seed=seed,
        stage1_sizes=tuple(stage1),
        stage2_sizes=tuple(stage2),
    )
    return samples, record
