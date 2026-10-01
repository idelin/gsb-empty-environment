"""Two-stage stratified cluster sampling (SRSWOR at both stages).

Design: in stratum h there are M_h clusters; m_h clusters are drawn at
stage one, then n_hi of the N_hi elements of each sampled cluster at
stage two.  Inclusion probabilities are
    pi_1h  = m_h / M_h            (cluster level)
    pi_2hi = n_hi / N_hi          (element level, conditional on cluster)
and both finite population corrections enter the variance estimator.

Cluster members are never treated as independent observations: the
estimator works on cluster total estimates, so within-cluster
correlation is handled by the between-cluster variance component.

Variance estimability is reported honestly:
  * m_h == 1 with M_h > 1  -> between-cluster component not estimable;
  * n_hi == 1 with N_hi > 1 -> within-cluster component not estimable;
  * fully enumerated stages contribute exactly 0 through the fpc;
  * a missing stratum or a zero inclusion probability is rejected.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Sequence

from .errors import (
    MissingStratumError,
    StratifiedSamplingError,
    ZeroInclusionProbabilityError,
)
from .incremental import IncrementalStatistics


@dataclass(frozen=True)
class ClusterSample:
    """Second-stage sample from one cluster: n_hi values out of N_hi."""

    population_size: int
    values: tuple[float, ...]

    def __init__(self, population_size: int, values: Sequence[float]):
        object.__setattr__(self, "population_size", population_size)
        object.__setattr__(self, "values", tuple(float(v) for v in values))
        if population_size < 1:
            raise StratifiedSamplingError("cluster population_size must be >= 1")
        if len(self.values) == 0:
            raise ZeroInclusionProbabilityError(
                "cluster with zero sampled elements has zero second-stage "
                "inclusion probability and is rejected"
            )
        if len(self.values) > population_size:
            raise StratifiedSamplingError(
                "cluster sample size exceeds cluster population size"
            )


@dataclass(frozen=True)
class ClusterStratumSample:
    """First-stage sample of clusters from one stratum of M_h clusters."""

    name: str
    population_clusters: int
    clusters: tuple[ClusterSample, ...]

    def __init__(self, name: str, population_clusters: int,
                 clusters: Sequence[ClusterSample]):
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "population_clusters", population_clusters)
        object.__setattr__(self, "clusters", tuple(clusters))
        if population_clusters < 1:
            raise StratifiedSamplingError(
                f"stratum {name!r}: population_clusters must be >= 1"
            )
        if len(self.clusters) == 0:
            raise MissingStratumError(
                f"stratum {name!r}: no clusters sampled; a missing stratum "
                "is rejected"
            )
        if len(self.clusters) > population_clusters:
            raise StratifiedSamplingError(
                f"stratum {name!r}: sampled clusters exceed population clusters"
            )

    @property
    def first_stage_inclusion_probability(self) -> float:
        return len(self.clusters) / self.population_clusters


@dataclass(frozen=True)
class ClusterStratumDiagnostic:
    name: str
    population_clusters: int
    sampled_clusters: int
    first_stage_inclusion_probability: float
    estimated_total: float
    between_component: float | None
    within_component: float | None
    variance: float | None
    variance_estimable: bool
    fully_enumerated: bool


@dataclass(frozen=True)
class TwoStageEstimate:
    total: float
    mean: float
    variance_total: float | None
    variance_mean: float | None
    standard_error: float | None
    variance_estimable: bool
    mean_denominator: str  # "known" or "estimated"
    mean_variance_approximate: bool
    strata: tuple[ClusterStratumDiagnostic, ...]


def estimate_two_stage_mean(
    strata: Sequence[ClusterStratumSample],
    total_population: int | None = None,
) -> TwoStageEstimate:
    """Two-stage stratified cluster mean estimator with diagnostics.

    If ``total_population`` is None the population size is itself
    estimated by the Horvitz-Thompson expansion of cluster sizes; the
    mean is then a ratio and its variance is flagged as approximate.
    """
    if not strata:
        raise StratifiedSamplingError("at least one stratum is required")

    total_terms: list[float] = []
    variance_terms: list[float] = []
    size_terms: list[float] = []
    diagnostics: list[ClusterStratumDiagnostic] = []
    all_estimable = True

    for stratum in strata:
        m_h = len(stratum.clusters)
        M_h = stratum.population_clusters
        f1 = 1.0 - m_h / M_h
        expansion = M_h / m_h  # inverse first-stage inclusion probability

        cluster_totals: list[float] = []
        within_terms: list[float] = []
        within_estimable = True
        for cluster in stratum.clusters:
            n_hi = len(cluster.values)
            N_hi = cluster.population_size
            stats = IncrementalStatistics(cluster.values)
            cluster_totals.append(N_hi * stats.mean)
            size_terms.append(expansion * N_hi)
            if n_hi == N_hi:
                continue  # fully enumerated cluster: zero contribution
            s2 = stats.sample_variance
            if s2 is None:
                # n_hi == 1 with N_hi > 1: honestly not estimable.
                within_estimable = False
            else:
                within_terms.append(
                    N_hi * N_hi * (1.0 - n_hi / N_hi) * s2 / n_hi
                )

        estimated_total = expansion * math.fsum(cluster_totals)
        total_terms.append(estimated_total)

        if f1 == 0.0:
            between: float | None = 0.0
        elif m_h >= 2:
            s2_between = IncrementalStatistics(cluster_totals).sample_variance
            between = M_h * M_h * f1 * s2_between / m_h
        else:
            between = None

        within = expansion * math.fsum(within_terms) if within_estimable else None

        if between is None or within is None:
            stratum_variance: float | None = None
            estimable = False
        else:
            stratum_variance = between + within
            variance_terms.append(stratum_variance)
            estimable = True
        all_estimable = all_estimable and estimable

        diagnostics.append(
            ClusterStratumDiagnostic(
                name=stratum.name,
                population_clusters=M_h,
                sampled_clusters=m_h,
                first_stage_inclusion_probability=m_h / M_h,
                estimated_total=estimated_total,
                between_component=between,
                within_component=within,
                variance=stratum_variance,
                variance_estimable=estimable,
                fully_enumerated=(f1 == 0.0 and all(
                    len(c.values) == c.population_size for c in stratum.clusters
                )),
            )
        )

    total = math.fsum(total_terms)
    variance_total = math.fsum(variance_terms) if all_estimable else None

    if total_population is not None:
        denominator = float(total_population)
        mean_denominator = "known"
        approximate = False
    else:
        denominator = math.fsum(size_terms)
        mean_denominator = "estimated"
        approximate = True

    mean = total / denominator
    variance_mean = (
        variance_total / (denominator * denominator)
        if variance_total is not None
        else None
    )
    return TwoStageEstimate(
        total=total,
        mean=mean,
        variance_total=variance_total,
        variance_mean=variance_mean,
        standard_error=(
            math.sqrt(variance_mean) if variance_mean is not None else None
        ),
        variance_estimable=all_estimable,
        mean_denominator=mean_denominator,
        mean_variance_approximate=approximate,
        strata=tuple(diagnostics),
    )
