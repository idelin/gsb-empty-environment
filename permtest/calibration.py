"""Permutation calibration for two-sample discrete statistics.

No normal approximation is used anywhere:

- Small designs are calibrated by *exact* enumeration of every label
  assignment (combinations for grouped designs, sign flips for paired
  designs). Enumeration is rank-sharded so it can run in parallel with
  no duplicated or omitted assignments.
- Large designs are calibrated by Monte Carlo sampling with a caller
  injected random source; the reported interval is an exact
  Clopper-Pearson binomial interval for the exceedance probability.

Exchangeability is part of the input design: the caller asserts it via
``exchangeable=True``. When the design is not exchangeable, calibration
is refused (:class:`ExchangeabilityError`) and no p-value is produced.

Results are labelled: ``method="exact"`` is an exact conclusion over the
full permutation reference; ``method="monte_carlo"`` is an empirical
estimate and carries a confidence interval.
"""
from __future__ import annotations

import math
import random
import statistics as _stats
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from math import comb
from typing import Callable, Sequence

from .combinatorics import iter_combination_shard, shard_ranges
from .statistics import ks_statistic, total_variation

Statistic = Callable[[list, list], float]

DEFAULT_EXACT_THRESHOLD = 100_000
DEFAULT_NUM_MC = 10_000


class ExchangeabilityError(Exception):
    """Raised when the input design does not support permutation calibration."""


def _mean_difference(xs: list, ys: list) -> float:
    return abs(_stats.fmean(xs) - _stats.fmean(ys))


_GROUPED_STATISTICS: dict[str, Statistic] = {
    "ks": ks_statistic,
    "tv": total_variation,
}

_PAIRED_STATISTICS: dict[str, Statistic] = {
    "mean_diff": _mean_difference,
    "ks": ks_statistic,
    "tv": total_variation,
}


@dataclass(frozen=True)
class CalibrationResult:
    """Outcome of a permutation calibration.

    ``method``/``exact`` label whether ``p_value`` is an exact conclusion
    over the full permutation reference or an empirical Monte Carlo
    estimate. ``seed`` and ``sizes`` record the random seed (if any) and
    the sample sizes used.
    """

    design: str
    statistic: str
    observed: float
    p_value: float
    method: str  # "exact" or "monte_carlo"
    exact: bool
    num_permutations: int
    exceedances: int
    seed: int | None
    sizes: tuple[int, ...]
    mc_interval: tuple[float, float] | None = None
    alpha: float = 0.05


# ---------------------------------------------------------------------------
# helpers


def _resolve_statistic(statistic, registry: dict[str, Statistic]):
    if callable(statistic):
        return statistic, getattr(statistic, "__name__", "custom")
    if isinstance(statistic, str) and statistic in registry:
        return registry[statistic], statistic
    raise ValueError(f"unknown statistic: {statistic!r}")


def _exceeds(value: float, observed: float) -> bool:
    return value > observed or math.isclose(
        value, observed, rel_tol=1e-9, abs_tol=1e-12
    )


def _run_sharded(work, ranges: list[tuple[int, int]], num_shards: int):
    if num_shards <= 1:
        return [work(*ranges[0])] if ranges else []
    with ThreadPoolExecutor(max_workers=num_shards) as pool:
        return list(pool.map(lambda r: work(*r), ranges))


def _partition(pool: list, combo: tuple[int, ...]):
    chosen = []
    rest = []
    ci = 0
    for index, value in enumerate(pool):
        if ci < len(combo) and combo[ci] == index:
            chosen.append(value)
            ci += 1
        else:
            rest.append(value)
    return chosen, rest


# ---------------------------------------------------------------------------
# exact Clopper-Pearson interval (no normal approximation)


def _log_binomial_pmf(i: int, n: int, p: float) -> float:
    if p <= 0.0:
        return 0.0 if i == 0 else -math.inf
    if p >= 1.0:
        return 0.0 if i == n else -math.inf
    return (
        math.lgamma(n + 1)
        - math.lgamma(i + 1)
        - math.lgamma(n - i + 1)
        + i * math.log(p)
        + (n - i) * math.log1p(-p)
    )


def _binomial_cdf(k: int, n: int, p: float) -> float:
    """P[X <= k] for X ~ Binomial(n, p)."""
    if k < 0:
        return 0.0
    if k >= n:
        return 1.0
    return min(1.0, math.fsum(math.exp(_log_binomial_pmf(i, n, p)) for i in range(k + 1)))


def _bisect_decreasing(func, target: float, low: float, high: float) -> float:
    for _ in range(80):
        mid = 0.5 * (low + high)
        if func(mid) > target:
            low = mid
        else:
            high = mid
    return 0.5 * (low + high)


def clopper_pearson(x: int, n: int, alpha: float = 0.05) -> tuple[float, float]:
    """Exact two-sided Clopper-Pearson interval for a binomial proportion."""
    if not 0 <= x <= n:
        raise ValueError("require 0 <= x <= n")
    if n < 1:
        raise ValueError("require n >= 1")
    if not 0 < alpha < 1:
        raise ValueError("require 0 < alpha < 1")
    if x == 0:
        low = 0.0
    else:
        low = _bisect_decreasing(
            lambda p: _binomial_cdf(x - 1, n, p), 1.0 - alpha / 2.0, 0.0, x / n
        )
    if x == n:
        high = 1.0
    else:
        high = _bisect_decreasing(
            lambda p: _binomial_cdf(x, n, p), alpha / 2.0, x / n, 1.0
        )
    return low, high


# ---------------------------------------------------------------------------
# grouped design


def _grouped_exact(pool: list, n_a: int, stat: Statistic, observed: float, num_shards: int):
    n = len(pool)
    total = comb(n, n_a)

    def work(start: int, stop: int):
        count = 0
        exceed = 0
        for combo in iter_combination_shard(n, n_a, start, stop):
            a_vals, b_vals = _partition(pool, combo)
            if _exceeds(stat(a_vals, b_vals), observed):
                exceed += 1
            count += 1
        return count, exceed

    partials = _run_sharded(work, shard_ranges(total, num_shards), num_shards)
    count = sum(part[0] for part in partials)
    exceed = sum(part[1] for part in partials)
    if count != total:
        raise AssertionError("shard enumeration duplicated or omitted assignments")
    return exceed, total


def _grouped_mc(pool: list, n_a: int, stat: Statistic, observed: float, draws: int, rng):
    n = len(pool)
    exceed = 0
    for _ in range(draws):
        chosen_idx = set(rng.sample(range(n), n_a))
        a_vals = [v for i, v in enumerate(pool) if i in chosen_idx]
        b_vals = [v for i, v in enumerate(pool) if i not in chosen_idx]
        if _exceeds(stat(a_vals, b_vals), observed):
            exceed += 1
    return exceed


def grouped_calibration(
    sample_a: Sequence,
    sample_b: Sequence,
    statistic="ks",
    *,
    exchangeable: bool = True,
    exact_threshold: int = DEFAULT_EXACT_THRESHOLD,
    num_mc: int = DEFAULT_NUM_MC,
    rng: random.Random | None = None,
    seed: int | None = None,
    num_shards: int = 1,
    alpha: float = 0.05,
) -> CalibrationResult:
    """Calibrate a two-sample statistic under a grouped (independent) design.

    Exchangeability: under the null, group labels are freely permutable
    across the pooled observations. Pass ``exchangeable=False`` when the
    design does not justify this; calibration is then refused.

    The random source is injected by the caller via ``rng`` (or built from
    ``seed``); the seed and sample sizes are recorded in the result.
    """
    if len(sample_a) == 0 or len(sample_b) == 0:
        raise ValueError("empty sample is not allowed")
    if not exchangeable:
        raise ExchangeabilityError(
            "grouped design is not exchangeable under label permutation; "
            "calibrated p-value refused"
        )
    stat, stat_name = _resolve_statistic(statistic, _GROUPED_STATISTICS)
    n_a, n_b = len(sample_a), len(sample_b)
    observed = float(stat(list(sample_a), list(sample_b)))
    # Canonical pooled order: results are invariant to input ordering.
    pool = sorted(list(sample_a) + list(sample_b))
    total = comb(n_a + n_b, n_a)

    if total <= exact_threshold:
        exceed, perms = _grouped_exact(pool, n_a, stat, observed, num_shards)
        return CalibrationResult(
            design="grouped",
            statistic=stat_name,
            observed=observed,
            p_value=exceed / perms,
            method="exact",
            exact=True,
            num_permutations=perms,
            exceedances=exceed,
            seed=seed,
            sizes=(n_a, n_b),
            mc_interval=None,
            alpha=alpha,
        )

    if rng is None:
        rng = random.Random(seed)
    exceed = _grouped_mc(pool, n_a, stat, observed, num_mc, rng)
    return CalibrationResult(
        design="grouped",
        statistic=stat_name,
        observed=observed,
        p_value=(exceed + 1) / (num_mc + 1),
        method="monte_carlo",
        exact=False,
        num_permutations=num_mc,
        exceedances=exceed,
        seed=seed,
        sizes=(n_a, n_b),
        mc_interval=clopper_pearson(exceed, num_mc, alpha),
        alpha=alpha,
    )


# ---------------------------------------------------------------------------
# paired design


def _apply_mask(pairs: list[tuple], mask: int):
    xs = []
    ys = []
    for i, (x, y) in enumerate(pairs):
        if (mask >> i) & 1:
            xs.append(y)
            ys.append(x)
        else:
            xs.append(x)
            ys.append(y)
    return xs, ys


def _paired_exact(pairs: list[tuple], stat: Statistic, observed: float, num_shards: int):
    m = len(pairs)
    total = 1 << m

    def work(start: int, stop: int):
        count = 0
        exceed = 0
        for mask in range(start, stop):
            xs, ys = _apply_mask(pairs, mask)
            if _exceeds(stat(xs, ys), observed):
                exceed += 1
            count += 1
        return count, exceed

    partials = _run_sharded(work, shard_ranges(total, num_shards), num_shards)
    count = sum(part[0] for part in partials)
    exceed = sum(part[1] for part in partials)
    if count != total:
        raise AssertionError("shard enumeration duplicated or omitted assignments")
    return exceed, total


def _paired_mc(pairs: list[tuple], stat: Statistic, observed: float, draws: int, rng):
    m = len(pairs)
    exceed = 0
    for _ in range(draws):
        mask = rng.getrandbits(m)
        xs, ys = _apply_mask(pairs, mask)
        if _exceeds(stat(xs, ys), observed):
            exceed += 1
    return exceed


def paired_calibration(
    sample_x: Sequence,
    sample_y: Sequence,
    statistic="mean_diff",
    *,
    exchangeable: bool = True,
    exact_threshold: int = DEFAULT_EXACT_THRESHOLD,
    num_mc: int = DEFAULT_NUM_MC,
    rng: random.Random | None = None,
    seed: int | None = None,
    num_shards: int = 1,
    alpha: float = 0.05,
) -> CalibrationResult:
    """Calibrate a statistic under a paired design.

    ``sample_x`` and ``sample_y`` are aligned: position i forms pair i.
    Exchangeability: under the null, the two values within each pair may
    be swapped independently. Pass ``exchangeable=False`` when the design
    does not justify this; calibration is then refused.
    """
    if len(sample_x) == 0 or len(sample_y) == 0:
        raise ValueError("empty sample is not allowed")
    if len(sample_x) != len(sample_y):
        raise ValueError("paired samples must have equal length")
    if not exchangeable:
        raise ExchangeabilityError(
            "paired design is not exchangeable under within-pair flips; "
            "calibrated p-value refused"
        )
    stat, stat_name = _resolve_statistic(statistic, _PAIRED_STATISTICS)
    m = len(sample_x)
    observed = float(stat(list(sample_x), list(sample_y)))
    # Canonical pair order: results are invariant to input ordering.
    pairs = sorted(zip(sample_x, sample_y))
    total = 1 << m

    if total <= exact_threshold:
        exceed, perms = _paired_exact(pairs, stat, observed, num_shards)
        return CalibrationResult(
            design="paired",
            statistic=stat_name,
            observed=observed,
            p_value=exceed / perms,
            method="exact",
            exact=True,
            num_permutations=perms,
            exceedances=exceed,
            seed=seed,
            sizes=(m,),
            mc_interval=None,
            alpha=alpha,
        )

    if rng is None:
        rng = random.Random(seed)
    exceed = _paired_mc(pairs, stat, observed, num_mc, rng)
    return CalibrationResult(
        design="paired",
        statistic=stat_name,
        observed=observed,
        p_value=(exceed + 1) / (num_mc + 1),
        method="monte_carlo",
        exact=False,
        num_permutations=num_mc,
        exceedances=exceed,
        seed=seed,
        sizes=(m,),
        mc_interval=clopper_pearson(exceed, num_mc, alpha),
        alpha=alpha,
    )
