"""One-stage stratified random sampling (SRSWOR within strata).

The estimator uses design weights W_h = N_h / N normalised to sum to 1.
Strata of different sizes are therefore never averaged naively: each
stratum mean enters with its population share.

Variance estimability rules (never faked):
  * a stratum with no observations -> MissingStratumError (rejected);
  * a stratum sampled to exhaustion (n_h == N_h) contributes exactly 0
    through the finite population correction, even when n_h == 1;
  * a stratum with n_h == 1 and N_h > 1 has no within-stratum variance
    estimate, so its contribution and the overall variance are reported
    as not estimable (None) instead of pretending precision.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Sequence

from .errors import MissingStratumError, StratifiedSamplingError
from .incremental import IncrementalStatistics


@dataclass(frozen=True)
class StratumSample:
    """Observations drawn from one stratum with known population size N_h."""

    name: str
    population_size: int
    values: tuple[float, ...]

    def __init__(self, name: str, population_size: int, values: Sequence[float]):
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "population_size", population_size)
        object.__setattr__(self, "values", tuple(float(v) for v in values))
        if population_size < 1:
            raise StratifiedSamplingError(
                f"stratum {name!r}: population_size must be >= 1"
            )
        if len(self.values) == 0:
            raise MissingStratumError(
                f"stratum {name!r}: no observations; a missing stratum "
                "cannot be estimated and is rejected"
            )
        if len(self.values) > population_size:
            raise StratifiedSamplingError(
                f"stratum {name!r}: sample size {len(self.values)} exceeds "
                f"population size {population_size}"
            )


@dataclass(frozen=True)
class StratumDiagnostic:
    name: str
    population_size: int
    sample_size: int
    weight: float
    mean: float
    sample_variance: float | None
    finite_population_correction: float
    variance_contribution: float | None
    variance_estimable: bool
    fully_sampled: bool


@dataclass(frozen=True)
class StratifiedEstimate:
    mean: float
    variance: float | None
    standard_error: float | None
    variance_estimable: bool
    total_population: int
    total_sample: int
    strata: tuple[StratumDiagnostic, ...]


def estimate_stratified_mean(samples: Sequence[StratumSample]) -> StratifiedEstimate:
    """Stratified mean estimator with honest variance estimability."""
    if not samples:
        raise StratifiedSamplingError("at least one stratum is required")
    names = [s.name for s in samples]
    if len(set(names)) != len(names):
        raise StratifiedSamplingError("stratum names must be unique")

    total_population = sum(s.population_size for s in samples)
    raw_weights = [s.population_size / total_population for s in samples]
    normaliser = math.fsum(raw_weights)
    weights = [w / normaliser for w in raw_weights]
    if not math.isclose(math.fsum(weights), 1.0, rel_tol=1e-12, abs_tol=1e-12):
        raise StratifiedSamplingError("stratum weights failed to normalise to 1")

    diagnostics: list[StratumDiagnostic] = []
    mean_terms: list[float] = []
    variance_terms: list[float] = []
    all_variance_estimable = True

    for sample, weight in zip(samples, weights):
        stats = IncrementalStatistics(sample.values)
        n_h = stats.count
        mean_h = stats.mean
        sample_variance = stats.sample_variance
        fully_sampled = n_h == sample.population_size
        fpc = 1.0 - n_h / sample.population_size

        if fully_sampled:
            # Census stratum: fpc == 0 forces an exactly zero contribution.
            contribution: float | None = 0.0
            estimable = True
        elif sample_variance is None:
            # n_h == 1 with N_h > 1: variance is genuinely not estimable.
            contribution = None
            estimable = False
        else:
            contribution = weight * weight * fpc * sample_variance / n_h
            estimable = True

        mean_terms.append(weight * mean_h)
        if contribution is None:
            all_variance_estimable = False
        else:
            variance_terms.append(contribution)

        diagnostics.append(
            StratumDiagnostic(
                name=sample.name,
                population_size=sample.population_size,
                sample_size=n_h,
                weight=weight,
                mean=mean_h,
                sample_variance=sample_variance,
                finite_population_correction=fpc,
                variance_contribution=contribution,
                variance_estimable=estimable,
                fully_sampled=fully_sampled,
            )
        )

    variance = math.fsum(variance_terms) if all_variance_estimable else None
    return StratifiedEstimate(
        mean=math.fsum(mean_terms),
        variance=variance,
        standard_error=math.sqrt(variance) if variance is not None else None,
        variance_estimable=all_variance_estimable,
        total_population=total_population,
        total_sample=sum(s.values.__len__() for s in samples),
        strata=tuple(diagnostics),
    )
