"""Monte Carlo bootstrap engine."""
from __future__ import annotations

import math
from dataclasses import dataclass

from .errors import InvalidResampleError
from .intervals import bca_interval, percentile_interval
from .rng import RandomSource

METHODS = ("percentile", "bca")
INVALID_POLICIES = ("raise", "omit")


@dataclass(frozen=True)
class BootstrapResult:
    """A Monte Carlo interval estimate (kind == "monte_carlo").

    Exact-enumeration results use kind == "exact" (see exact.py); the two are
    always labelled separately.
    """

    method: str
    kind: str
    plan: str
    estimate: float
    lower: float
    upper: float
    alpha: float
    degenerate: bool
    n_resamples: int
    invalid_count: int
    seed: int
    n_units: int

    @property
    def effective_resamples(self) -> int:
        return self.n_resamples - self.invalid_count

    def __str__(self) -> str:
        deg = ", DEGENERATE (point interval)" if self.degenerate else ""
        return (
            f"[{self.kind}] {self.method} {1 - self.alpha:.0%} CI "
            f"({self.plan} plan): ({self.lower:.6g}, {self.upper:.6g})"
            f" | estimate={self.estimate:.6g} n_units={self.n_units}"
            f" B={self.n_resamples} invalid={self.invalid_count}"
            f" seed={self.seed}{deg}"
        )


def _is_finite_number(value) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) \
        and math.isfinite(value)


def bootstrap(plan, statistic, *, n_resamples: int, rng: RandomSource,
              alpha: float = 0.05, method: str = "percentile",
              invalid_policy: str = "raise") -> BootstrapResult:
    """Monte Carlo bootstrap confidence interval.

    Parameters
    ----------
    plan : a resampling plan from plans.py (defines the observational unit).
    statistic : callable on a resample payload, must return a finite float.
    n_resamples : number of bootstrap replicates B (int >= 2).
    rng : RandomSource injected by the caller; its seed is recorded.
    alpha : nominal error level; the interval targets 1 - alpha coverage.
    method : "percentile" or "bca" (separate implementations).
    invalid_policy : "raise" (default) aborts on the first invalid replicate;
        "omit" skips invalid replicates and reports ``invalid_count``
        explicitly in the result.  Invalid replicates are never dropped
        silently, because that would change the effective B and hence the
        achieved confidence level.
    """
    if not isinstance(rng, RandomSource):
        raise TypeError("rng must be a RandomSource injected by the caller")
    if not isinstance(n_resamples, int) or isinstance(n_resamples, bool) \
            or n_resamples < 2:
        raise ValueError(f"n_resamples must be an int >= 2, got {n_resamples!r}")
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha must be in (0, 1), got {alpha!r}")
    if method not in METHODS:
        raise ValueError(f"method must be one of {METHODS}, got {method!r}")
    if invalid_policy not in INVALID_POLICIES:
        raise ValueError(
            f"invalid_policy must be one of {INVALID_POLICIES}, got {invalid_policy!r}")

    observed = statistic(plan.full_sample())
    if not _is_finite_number(observed):
        raise InvalidResampleError(
            f"observed statistic is not finite: {observed!r}")
    observed = float(observed)

    boot = []
    invalid = 0
    for rep in range(n_resamples):
        stream = rng.stream(rep)  # keyed by replicate ID: order-independent
        payload = plan.resample(stream)
        try:
            value = statistic(payload)
        except Exception as exc:
            if invalid_policy == "raise":
                raise InvalidResampleError(
                    f"statistic raised on replicate {rep}: {exc!r}",
                    replicate_id=rep) from exc
            invalid += 1
            continue
        if not _is_finite_number(value):
            if invalid_policy == "raise":
                raise InvalidResampleError(
                    f"statistic returned a non-finite value ({value!r}) "
                    f"on replicate {rep}", replicate_id=rep)
            invalid += 1
            continue
        boot.append(float(value))

    if not boot:
        raise InvalidResampleError(
            "every replicate was invalid; no interval can be formed")

    boot.sort()
    degenerate = boot[0] == boot[-1]

    if method == "percentile":
        lower, upper = percentile_interval(boot, alpha)
    else:
        jackknife_stats = []
        for i, payload in enumerate(plan.jackknife()):
            value = statistic(payload)
            if not _is_finite_number(value):
                raise InvalidResampleError(
                    f"jackknife replicate {i} is not finite: {value!r}")
            jackknife_stats.append(float(value))
        lower, upper = bca_interval(boot, observed, jackknife_stats, alpha)

    return BootstrapResult(
        method=method,
        kind="monte_carlo",
        plan=plan.kind,
        estimate=observed,
        lower=lower,
        upper=upper,
        alpha=alpha,
        degenerate=degenerate,
        n_resamples=n_resamples,
        invalid_count=invalid,
        seed=rng.seed,
        n_units=plan.n_units,
    )
