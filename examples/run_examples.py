"""Runnable examples for the stratified_sampling package.

Every section states whether its conclusions are EXACT (finite
population enumeration) or EMPIRICAL (Monte-Carlo replication with an
injected RNG).
"""

import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from stratified_sampling import (
    ClusterSample,
    ClusterStratumSample,
    IncrementalStatistics,
    StratumSample,
    draw_stratified_sample,
    empirical_stratified_distribution,
    estimate_stratified_mean,
    estimate_two_stage_mean,
    exact_stratified_distribution,
    exact_two_stage_distribution,
)


def section(title):
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


def example_stratified():
    section("1. One-stage stratified mean (weights sum to 1, unequal strata)")
    estimate = estimate_stratified_mean([
        StratumSample("urban", population_size=800, values=[1.0, 2.0]),
        StratumSample("rural", population_size=200, values=[3.0, 4.0]),
    ])
    for diag in estimate.strata:
        print(f"  stratum {diag.name:6s} N={diag.population_size:4d} "
              f"n={diag.sample_size} weight={diag.weight:.3f} "
              f"mean={diag.mean:.3f} contribution={diag.variance_contribution:.6f}")
    print(f"  weighted mean      = {estimate.mean:.4f} "
          f"(naive average of stratum means would be 2.5)")
    print(f"  variance           = {estimate.variance:.6f}")
    print(f"  standard error     = {estimate.standard_error:.6f}")
    print(f"  variance estimable = {estimate.variance_estimable}")


def example_single_sample_stratum():
    section("2. Single-sample stratum: variance honestly NOT estimable")
    estimate = estimate_stratified_mean([
        StratumSample("thin", population_size=500, values=[7.0]),
        StratumSample("ok", population_size=500, values=[1.0, 2.0, 3.0]),
    ])
    print(f"  mean               = {estimate.mean:.4f} (point estimate still valid)")
    print(f"  variance           = {estimate.variance} "
          f"(no fake precision for n=1, N>1)")
    print(f"  variance estimable = {estimate.variance_estimable}")


def example_two_stage():
    section("3. Two-stage stratified cluster sampling with diagnostics")
    strata = [
        ClusterStratumSample(
            "north",
            population_clusters=4,
            clusters=[
                ClusterSample(10, [1.0, 2.0, 3.0, 4.0, 5.0]),
                ClusterSample(10, [5.0, 6.0, 7.0, 8.0, 9.0]),
            ],
        ),
        ClusterStratumSample(
            "south",
            population_clusters=3,
            clusters=[
                ClusterSample(8, [2.0, 4.0, 6.0]),
                ClusterSample(8, [3.0, 5.0, 7.0]),
            ],
        ),
    ]
    estimate = estimate_two_stage_mean(strata, total_population=64)
    for diag in estimate.strata:
        print(f"  stratum {diag.name:6s} M={diag.population_clusters} "
              f"m={diag.sampled_clusters} "
              f"pi_1={diag.first_stage_inclusion_probability:.3f} "
              f"between={diag.between_component:.4f} "
              f"within={diag.within_component:.4f}")
    print(f"  total estimate     = {estimate.total:.4f}")
    print(f"  mean estimate      = {estimate.mean:.4f} "
          f"(denominator: {estimate.mean_denominator})")
    print(f"  variance of mean   = {estimate.variance_mean:.6f}")
    print(f"  standard error     = {estimate.standard_error:.6f}")
    print(f"  variance estimable = {estimate.variance_estimable}")


def example_incremental():
    section("4. Incremental retraction when sample identity changes")
    stats = IncrementalStatistics([10.0, 20.0, 30.0, 40.0])
    print(f"  before swap: n={stats.count} mean={stats.mean:.4f} "
          f"s^2={stats.sample_variance:.4f}")
    stats.retract(20.0)
    stats.add(25.0)
    print(f"  after swap : n={stats.count} mean={stats.mean:.4f} "
          f"s^2={stats.sample_variance:.4f} "
          f"(20 retracted, 25 added, no full recompute)")


def example_exact_vs_empirical():
    section("5. EXACT enumeration vs EMPIRICAL replication (labelled)")
    population = {
        "urban": [1.0, 2.0, 3.0, 4.0, 5.0],
        "rural": [10.0, 20.0, 30.0],
    }
    sizes = {"urban": 2, "rural": 2}

    exact = exact_stratified_distribution(population, sizes)
    print(f"  [EXACT] designs={exact.num_designs} "
          f"E[est]={exact.expectation:.6f} true={exact.true_mean:.6f} "
          f"unbiased={exact.unbiased}")
    print(f"  [EXACT] Var[est]={exact.variance:.6f} "
          f"theory={exact.theoretical_variance:.6f} "
          f"matches={exact.variance_matches_theory}")
    print(f"  [EXACT] E[var estimator]={exact.mean_variance_estimate:.6f} "
          f"unbiased={exact.variance_estimator_unbiased}")

    rng = random.Random(20261001)
    empirical = empirical_stratified_distribution(
        population, sizes, replications=20000, rng=rng
    )
    print(f"  [EMPIRICAL] replications={empirical.num_designs} "
          f"E[est]~{empirical.expectation:.6f} "
          f"Var[est]~{empirical.variance:.6f}")

    cluster_population = {
        "north": [[1.0, 1.0, 2.0], [5.0, 5.0, 6.0], [9.0, 9.0, 10.0]],
        "south": [[2.0, 3.0, 4.0], [8.0, 9.0, 10.0]],
    }
    two_stage = exact_two_stage_distribution(
        cluster_population, {"north": 2, "south": 2}, {"north": 2, "south": 2}
    )
    print(f"  [EXACT two-stage] designs={two_stage.num_designs} "
          f"E[est]={two_stage.expectation:.6f} "
          f"true={two_stage.true_mean:.6f} unbiased={two_stage.unbiased}")
    print(f"  [EXACT two-stage] Var[est]={two_stage.variance:.6f} "
          f"theory={two_stage.theoretical_variance:.6f} "
          f"matches={two_stage.variance_matches_theory}")


def example_sampling_record():
    section("6. Injected RNG with recorded seed and sample sizes")
    population = {"a": [1.0, 2.0, 3.0, 4.0], "b": [5.0, 6.0, 7.0]}
    samples, record = draw_stratified_sample(
        population, {"a": 2, "b": 2}, rng=random.Random(99), seed=99
    )
    print(f"  seed={record.seed} sizes={dict(record.stage1_sizes)}")
    for sample in samples:
        print(f"  stratum {sample.name}: drew {list(sample.values)}")


if __name__ == "__main__":
    example_stratified()
    example_single_sample_stratum()
    example_two_stage()
    example_incremental()
    example_exact_vs_empirical()
    example_sampling_record()
