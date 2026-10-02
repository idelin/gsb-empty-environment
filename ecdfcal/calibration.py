"""Permutation calibration of two-sample statistics, no normal approximation.

Designs (exchangeability assumptions are part of the *input design*):
    UNPAIRED_EXCHANGEABLE: group labels are exchangeable; the reference set
        is all C(n+m, n) re-labellings (or Monte Carlo draws from them).
    PAIRED_EXCHANGEABLE: within-pair labels are exchangeable; the reference
        set is all 2^m sign flips (or Monte Carlo draws from them).
    NOT_EXCHANGEABLE: no exchangeability assumption; the calibrated p-value
        is refused (observed statistic is still reported).

Small designs are enumerated exactly (optionally in rank-sharded parallel);
large designs use caller-injected Monte Carlo randomness and report an exact
Clopper-Peerson binomial interval.  Exact conclusions and Monte Carlo
estimates are labelled separately via CalibrationResult.kind.
"""
from __future__ import annotations

import multiprocessing as mp
from concurrent.futures import ProcessPoolExecutor
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import comb, exp, lgamma, log1p, log
from typing import Optional

from .combinatorics import shard_ranges, unrank_combination
from .ecdf import ECDF
from .statistics import ks_statistic, tv_distance


class Design(Enum):
    UNPAIRED_EXCHANGEABLE = "unpaired_exchangeable"
    PAIRED_EXCHANGEABLE = "paired_exchangeable"
    NOT_EXCHANGEABLE = "not_exchangeable"


class Statistic(Enum):
    KS = "ks"
    TV = "tv"


_STAT_FUNCS = {Statistic.KS: ks_statistic, Statistic.TV: tv_distance}


@dataclass(frozen=True)
class CalibrationResult:
    design: Design
    statistic: Statistic
    observed: Fraction
    p_value: Optional[Fraction]      # None when calibration is refused
    kind: Optional[str]              # "exact" | "monte_carlo" | None (refused)
    exceedances: Optional[int]       # reference permutations with T* >= observed
    reference_size: Optional[int]    # total permutations (exact) or MC draws
    ci_low: Optional[float]          # Clopper-Peerson interval (monte_carlo only)
    ci_high: Optional[float]
    confidence: Optional[float]
    seed: Optional[int]              # caller-declared seed of the injected RNG
    refused: bool
    refusal_reason: Optional[str]


# --------------------------------------------------------------------------
# Shard workers (module level so they are picklable for process pools).
# --------------------------------------------------------------------------

def _unpaired_shard(args):
    pooled, n_a, stat_key, observed, low, high = args
    stat_fn = _STAT_FUNCS[Statistic(stat_key)]
    n = len(pooled)
    count = 0
    for rank in range(low, high):
        idx = unrank_combination(rank, n, n_a)
        chosen = set(idx)
        ecdf_a = ECDF.from_samples(pooled[i] for i in idx)
        ecdf_b = ECDF.from_samples(pooled[i] for i in range(n) if i not in chosen)
        if stat_fn(ecdf_a, ecdf_b) >= observed:
            count += 1
    return count


def _paired_shard(args):
    pairs, stat_key, observed, low, high = args
    stat_fn = _STAT_FUNCS[Statistic(stat_key)]
    m = len(pairs)
    count = 0
    for rank in range(low, high):
        sample_a, sample_b = [], []
        for i in range(m):
            x, y = pairs[i]
            if (rank >> i) & 1:
                sample_a.append(y)
                sample_b.append(x)
            else:
                sample_a.append(x)
                sample_b.append(y)
        ecdf_a = ECDF.from_samples(sample_a)
        ecdf_b = ECDF.from_samples(sample_b)
        if stat_fn(ecdf_a, ecdf_b) >= observed:
            count += 1
    return count


def _enumerate(worker, base_args, total, n_shards, parallel):
    ranges = shard_ranges(total, n_shards)
    args = [base_args + (low, high) for low, high in ranges]
    if parallel and len(ranges) > 1:
        try:
            context = mp.get_context("fork")
        except ValueError:  # platform without fork (e.g. Windows)
            context = mp.get_context()
        with ProcessPoolExecutor(mp_context=context) as pool:
            return sum(pool.map(worker, args))
    return sum(worker(arg) for arg in args)


# --------------------------------------------------------------------------
# Exact Clopper-Peerson interval (bisection on the binomial tail; the
# normal approximation is never used).
# --------------------------------------------------------------------------

def _log_binom_pmf(k, n, p):
    return (
        lgamma(n + 1) - lgamma(k + 1) - lgamma(n - k + 1)
        + k * log(p) + (n - k) * log1p(-p)
    )


def _binom_sum(n, p, predicate):
    """Sum of Binomial(n, p) pmf over k satisfying predicate, tails skipped."""
    if p <= 0.0:
        return 1.0 if predicate(0) else 0.0
    if p >= 1.0:
        return 1.0 if predicate(n) else 0.0
    mode = min(max(int((n + 1) * p), 0), n)
    peak = _log_binom_pmf(mode, n, p)
    total = 0.0
    k = mode
    while k >= 0:
        rel = _log_binom_pmf(k, n, p) - peak
        if rel < -45.0:
            break
        if predicate(k):
            total += exp(rel)
        k -= 1
    k = mode + 1
    while k <= n:
        rel = _log_binom_pmf(k, n, p) - peak
        if rel < -45.0:
            break
        if predicate(k):
            total += exp(rel)
        k += 1
    return min(1.0, total * exp(peak))


def clopper_pearson_interval(x, n, confidence=0.95):
    """Exact two-sided Clopper-Peerson interval for a binomial proportion."""
    if not 0 <= x <= n:
        raise ValueError("require 0 <= x <= n")
    alpha = 1.0 - confidence

    def solve(decreasing_fn):
        # Bisection root of a function decreasing from + to - on [0, 1].
        lo, hi = 0.0, 1.0
        for _ in range(80):
            mid = (lo + hi) / 2
            if decreasing_fn(mid) > 0:
                lo = mid
            else:
                hi = mid
        return (lo + hi) / 2

    if x == 0:
        low = 0.0
    else:
        # P_p[X >= x] is increasing in p; find p where it equals alpha/2.
        low = solve(lambda p: alpha / 2 - _binom_sum(n, p, lambda k: k >= x))
    if x == n:
        high = 1.0
    else:
        # P_p[X <= x] is decreasing in p; find p where it equals alpha/2.
        high = solve(lambda p: _binom_sum(n, p, lambda k: k <= x) - alpha / 2)
    return low, high


# --------------------------------------------------------------------------
# Main entry point.
# --------------------------------------------------------------------------

def _resolve_rng(rng, seed):
    if rng is not None:
        return rng
    if seed is None:
        raise ValueError(
            "Monte Carlo calibration requires an injected random source "
            "(pass rng=random.Random(...) or seed=...)"
        )
    import random

    return random.Random(seed)


def calibrate(sample_a, sample_b, *, design, statistic=Statistic.KS,
              method="auto", max_exact=50_000, n_permutations=10_000,
              seed=None, rng=None, confidence=0.95,
              n_shards=1, parallel=False) -> CalibrationResult:
    """Calibrate `statistic` between two samples under the given design.

    sample_a / sample_b are the two groups (unpaired design) or the two
    aligned columns of m pairs (paired design, equal lengths required).
    Input order is canonicalised, so re-ordering labels never changes the
    result.  Empty samples are rejected.  When the design does not justify
    exchangeability, the calibrated p-value is refused while the observed
    statistic is still reported.
    """
    if method not in ("auto", "exact", "monte_carlo"):
        raise ValueError("method must be 'auto', 'exact' or 'monte_carlo'")
    stat_fn = _STAT_FUNCS[statistic]

    if design is Design.PAIRED_EXCHANGEABLE:
        if len(sample_a) != len(sample_b):
            raise ValueError("paired design requires equal-length samples")
        pairs = sorted((x, y) for x, y in zip(sample_a, sample_b))
        if not pairs:
            raise ValueError("empty sample is not allowed")
        observed = stat_fn(
            ECDF.from_samples(x for x, _ in pairs),
            ECDF.from_samples(y for _, y in pairs),
        )
        return _calibrate_paired(pairs, observed, statistic, stat_fn,
                                 1 << len(pairs), method, max_exact,
                                 n_permutations, seed, rng, confidence,
                                 n_shards, parallel)

    if design in (Design.UNPAIRED_EXCHANGEABLE, Design.NOT_EXCHANGEABLE):
        a = sorted(sample_a)
        b = sorted(sample_b)
        if not a or not b:
            raise ValueError("empty sample is not allowed")
        observed = stat_fn(ECDF.from_samples(a), ECDF.from_samples(b))
        if design is Design.NOT_EXCHANGEABLE:
            return CalibrationResult(
                design=design, statistic=statistic, observed=observed,
                p_value=None, kind=None, exceedances=None, reference_size=None,
                ci_low=None, ci_high=None, confidence=None, seed=None,
                refused=True,
                refusal_reason=(
                    "exchangeability is not justified by the input design; "
                    "calibrated p-value refused"
                ),
            )
        pooled = a + b
        return _calibrate_unpaired(pooled, len(a), observed, statistic,
                                   stat_fn, comb(len(pooled), len(a)), method,
                                   max_exact, n_permutations, seed, rng,
                                   confidence, n_shards, parallel)

    raise ValueError(f"unknown design: {design}")


def _calibrate_unpaired(pooled, n_a, observed, statistic, stat_fn, total,
                        method, max_exact, n_permutations, seed, rng,
                        confidence, n_shards, parallel):
    n = len(pooled)
    exact = method == "exact" or (method == "auto" and total <= max_exact)
    if exact:
        exceed = _enumerate(
            _unpaired_shard, (pooled, n_a, statistic.value, observed),
            total, n_shards, parallel,
        )
        return CalibrationResult(
            design=Design.UNPAIRED_EXCHANGEABLE, statistic=statistic,
            observed=observed, p_value=Fraction(exceed, total), kind="exact",
            exceedances=exceed, reference_size=total,
            ci_low=None, ci_high=None, confidence=None, seed=None,
            refused=False, refusal_reason=None,
        )
    generator = _resolve_rng(rng, seed)
    exceed = 0
    for _ in range(n_permutations):
        chosen = set(generator.sample(range(n), n_a))
        ecdf_a = ECDF.from_samples(pooled[i] for i in range(n) if i in chosen)
        ecdf_b = ECDF.from_samples(pooled[i] for i in range(n) if i not in chosen)
        if stat_fn(ecdf_a, ecdf_b) >= observed:
            exceed += 1
    low, high = clopper_pearson_interval(exceed, n_permutations, confidence)
    return CalibrationResult(
        design=Design.UNPAIRED_EXCHANGEABLE, statistic=statistic,
        observed=observed,
        p_value=Fraction(exceed + 1, n_permutations + 1), kind="monte_carlo",
        exceedances=exceed, reference_size=n_permutations,
        ci_low=low, ci_high=high, confidence=confidence, seed=seed,
        refused=False, refusal_reason=None,
    )


def _calibrate_paired(pairs, observed, statistic, stat_fn, total,
                      method, max_exact, n_permutations, seed, rng,
                      confidence, n_shards, parallel):
    m = len(pairs)
    exact = method == "exact" or (method == "auto" and total <= max_exact)
    if exact:
        exceed = _enumerate(
            _paired_shard, (pairs, statistic.value, observed),
            total, n_shards, parallel,
        )
        return CalibrationResult(
            design=Design.PAIRED_EXCHANGEABLE, statistic=statistic,
            observed=observed, p_value=Fraction(exceed, total), kind="exact",
            exceedances=exceed, reference_size=total,
            ci_low=None, ci_high=None, confidence=None, seed=None,
            refused=False, refusal_reason=None,
        )
    generator = _resolve_rng(rng, seed)
    exceed = 0
    for _ in range(n_permutations):
        bits = generator.getrandbits(m)
        sample_a, sample_b = [], []
        for i in range(m):
            x, y = pairs[i]
            if (bits >> i) & 1:
                sample_a.append(y)
                sample_b.append(x)
            else:
                sample_a.append(x)
                sample_b.append(y)
        if stat_fn(ECDF.from_samples(sample_a), ECDF.from_samples(sample_b)) >= observed:
            exceed += 1
    low, high = clopper_pearson_interval(exceed, n_permutations, confidence)
    return CalibrationResult(
        design=Design.PAIRED_EXCHANGEABLE, statistic=statistic,
        observed=observed,
        p_value=Fraction(exceed + 1, n_permutations + 1), kind="monte_carlo",
        exceedances=exceed, reference_size=n_permutations,
        ci_low=low, ci_high=high, confidence=confidence, seed=seed,
        refused=False, refusal_reason=None,
    )
