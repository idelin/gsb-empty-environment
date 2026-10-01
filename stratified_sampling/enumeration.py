"""Design-based verification by finite population enumeration.

For small populations every possible sample design can be enumerated,
giving *exact* statements (labelled kind="exact") about the expectation
and variance of an estimator.  Large designs are checked by Monte-Carlo
replication with an injected RNG (labelled kind="empirical").  The two
kinds of conclusion are never mixed.
"""

from __future__ import annotations

import itertools
import math
import random
from dataclasses import dataclass
from typing import Iterator, Mapping, Sequence

from .cluster import ClusterSample, ClusterStratumSample, estimate_two_stage_mean
from .incremental import IncrementalStatistics
from .stratified import StratumSample, estimate_stratified_mean

EXACT = "exact"
EMPIRICAL = "empirical"


@dataclass(frozen=True)
class DistributionSummary:
    """Distribution of an estimator over all (or replicated) designs."""

    kind: str  # EXACT or EMPIRICAL
    num_designs: int
    true_mean: float
    expectation: float
    variance: float
    theoretical_variance: float
    mean_variance_estimate: float | None
    unbiased: bool
    variance_matches_theory: bool
    variance_estimator_unbiased: bool | None


def iter_stratified_designs(
    population: Mapping[str, Sequence[float]],
    sample_sizes: Mapping[str, int],
) -> Iterator[dict[str, tuple[float, ...]]]:
    """Yield every stratified sample design (Cartesian product of combinations)."""
    names = list(population)
    per_stratum = [
        list(itertools.combinations(population[name], sample_sizes[name]))
        for name in names
    ]
    for combo in itertools.product(*per_stratum):
        yield dict(zip(names, combo))


def theoretical_stratified_variance(
    population: Mapping[str, Sequence[float]],
    sample_sizes: Mapping[str, int],
) -> float:
    """Exact design variance sum_h W_h^2 (1 - f_h) S_h^2 / n_h (S_h^2 ddof=1)."""
    total_n = sum(len(v) for v in population.values())
    terms = []
    for name, values in population.items():
        N_h = len(values)
        n_h = sample_sizes[name]
        weight = N_h / total_n
        if n_h == N_h:
            continue  # census stratum contributes exactly 0
        stats = IncrementalStatistics(values)
        S2 = stats.sample_variance  # ddof=1 population variance
        terms.append(weight * weight * (1.0 - n_h / N_h) * S2 / n_h)
    return math.fsum(terms)


def exact_stratified_distribution(
    population: Mapping[str, Sequence[float]],
    sample_sizes: Mapping[str, int],
) -> DistributionSummary:
    """Enumerate all designs: exact E and Var of the stratified mean estimator."""
    true_mean = math.fsum(
        math.fsum(map(float, v)) for v in population.values()
    ) / sum(len(v) for v in population.values())
    theoretical = theoretical_stratified_variance(population, sample_sizes)

    estimates = IncrementalStatistics()
    variance_estimates = IncrementalStatistics()
    num_designs = 0
    for design in iter_stratified_designs(population, sample_sizes):
        samples = [
            StratumSample(name, len(population[name]), values)
            for name, values in design.items()
        ]
        result = estimate_stratified_mean(samples)
        estimates.add(result.mean)
        if result.variance is not None:
            variance_estimates.add(result.variance)
        num_designs += 1

    expectation = estimates.mean
    variance = estimates.population_variance
    tol = 1e-9
    mean_var_est = (
        variance_estimates.mean if variance_estimates.count == num_designs else None
    )
    return DistributionSummary(
        kind=EXACT,
        num_designs=num_designs,
        true_mean=true_mean,
        expectation=expectation,
        variance=variance,
        theoretical_variance=theoretical,
        mean_variance_estimate=mean_var_est,
        unbiased=math.isclose(expectation, true_mean, rel_tol=tol, abs_tol=tol),
        variance_matches_theory=math.isclose(
            variance, theoretical, rel_tol=tol, abs_tol=tol
        ),
        variance_estimator_unbiased=(
            None
            if mean_var_est is None
            else math.isclose(mean_var_est, variance, rel_tol=tol, abs_tol=tol)
        ),
    )


def empirical_stratified_distribution(
    population: Mapping[str, Sequence[float]],
    sample_sizes: Mapping[str, int],
    *,
    replications: int,
    rng: random.Random,
) -> DistributionSummary:
    """Monte-Carlo replication with an injected RNG (empirical, not exact)."""
    from .sampling import draw_stratified_sample

    true_mean = math.fsum(
        math.fsum(map(float, v)) for v in population.values()
    ) / sum(len(v) for v in population.values())
    theoretical = theoretical_stratified_variance(population, sample_sizes)

    estimates = IncrementalStatistics()
    variance_estimates = IncrementalStatistics()
    for _ in range(replications):
        samples, _record = draw_stratified_sample(population, sample_sizes, rng=rng)
        result = estimate_stratified_mean(samples)
        estimates.add(result.mean)
        if result.variance is not None:
            variance_estimates.add(result.variance)

    expectation = estimates.mean
    variance = estimates.population_variance
    mean_var_est = (
        variance_estimates.mean if variance_estimates.count == replications else None
    )
    return DistributionSummary(
        kind=EMPIRICAL,
        num_designs=replications,
        true_mean=true_mean,
        expectation=expectation,
        variance=variance,
        theoretical_variance=theoretical,
        mean_variance_estimate=mean_var_est,
        unbiased=math.isclose(expectation, true_mean, rel_tol=0.05, abs_tol=0.05),
        variance_matches_theory=math.isclose(
            variance, theoretical, rel_tol=0.10, abs_tol=0.10
        ),
        variance_estimator_unbiased=(
            None
            if mean_var_est is None
            else math.isclose(mean_var_est, variance, rel_tol=0.10, abs_tol=0.10)
        ),
    )


def iter_two_stage_designs(
    population: Mapping[str, Sequence[Sequence[float]]],
    first_stage_sizes: Mapping[str, int],
    second_stage_sizes: Mapping[str, int],
) -> Iterator[dict[str, tuple[tuple[int, tuple[float, ...]], ...]]]:
    """Yield every two-stage design as {stratum: ((cluster_idx, elements), ...)}."""
    names = list(population)
    per_stratum_designs = []
    for name in names:
        clusters = population[name]
        m_h = first_stage_sizes[name]
        n_h = second_stage_sizes[name]
        stratum_designs = []
        for cluster_idx in itertools.combinations(range(len(clusters)), m_h):
            element_combos = [
                list(itertools.combinations(clusters[i], n_h)) for i in cluster_idx
            ]
            for elements in itertools.product(*element_combos):
                stratum_designs.append(tuple(zip(cluster_idx, elements)))
        per_stratum_designs.append(stratum_designs)
    for design in itertools.product(*per_stratum_designs):
        yield dict(zip(names, design))


def theoretical_two_stage_variance(
    population: Mapping[str, Sequence[Sequence[float]]],
    first_stage_sizes: Mapping[str, int],
    second_stage_sizes: Mapping[str, int],
) -> float:
    """Exact design variance of the two-stage total estimator."""
    terms = []
    for name, clusters in population.items():
        M_h = len(clusters)
        m_h = first_stage_sizes[name]
        n_h = second_stage_sizes[name]
        totals = [math.fsum(map(float, c)) for c in clusters]
        if m_h < M_h:
            S2_between = IncrementalStatistics(totals).sample_variance
            terms.append(M_h * M_h * (1.0 - m_h / M_h) * S2_between / m_h)
        within = []
        for cluster in clusters:
            N_hi = len(cluster)
            if n_h == N_hi:
                continue
            S2_within = IncrementalStatistics(cluster).sample_variance
            within.append(N_hi * N_hi * (1.0 - n_h / N_hi) * S2_within / n_h)
        terms.append((M_h / m_h) * math.fsum(within))
    return math.fsum(terms)


def exact_two_stage_distribution(
    population: Mapping[str, Sequence[Sequence[float]]],
    first_stage_sizes: Mapping[str, int],
    second_stage_sizes: Mapping[str, int],
) -> DistributionSummary:
    """Enumerate all two-stage designs: exact moments of the mean estimator."""
    total_population = sum(
        len(cluster) for clusters in population.values() for cluster in clusters
    )
    true_mean = math.fsum(
        math.fsum(map(float, cluster))
        for clusters in population.values()
        for cluster in clusters
    ) / total_population
    theoretical_total = theoretical_two_stage_variance(
        population, first_stage_sizes, second_stage_sizes
    )
    theoretical = theoretical_total / (total_population * total_population)

    estimates = IncrementalStatistics()
    variance_estimates = IncrementalStatistics()
    num_designs = 0
    for design in iter_two_stage_designs(
        population, first_stage_sizes, second_stage_sizes
    ):
        strata = []
        for name, cluster_designs in design.items():
            clusters = [
                ClusterSample(len(population[name][idx]), elements)
                for idx, elements in cluster_designs
            ]
            strata.append(
                ClusterStratumSample(name, len(population[name]), clusters)
            )
        result = estimate_two_stage_mean(strata, total_population=total_population)
        estimates.add(result.mean)
        if result.variance_mean is not None:
            variance_estimates.add(result.variance_mean)
        num_designs += 1

    expectation = estimates.mean
    variance = estimates.population_variance
    tol = 1e-9
    mean_var_est = (
        variance_estimates.mean if variance_estimates.count == num_designs else None
    )
    return DistributionSummary(
        kind=EXACT,
        num_designs=num_designs,
        true_mean=true_mean,
        expectation=expectation,
        variance=variance,
        theoretical_variance=theoretical,
        mean_variance_estimate=mean_var_est,
        unbiased=math.isclose(expectation, true_mean, rel_tol=tol, abs_tol=tol),
        variance_matches_theory=math.isclose(
            variance, theoretical, rel_tol=tol, abs_tol=tol
        ),
        variance_estimator_unbiased=(
            None
            if mean_var_est is None
            else math.isclose(mean_var_est, variance, rel_tol=tol, abs_tol=tol)
        ),
    )
