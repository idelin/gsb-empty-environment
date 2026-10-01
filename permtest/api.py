"""High-level entry points for the three supported designs.

Each function returns either an ExactResult (labelled
"exact (full enumeration)") or a MonteCarloResult (labelled
"monte-carlo (approximate)") -- exact conclusions and empirical
estimates are never conflated.
"""
from __future__ import annotations

from typing import Optional, Sequence, Tuple

from .exact import ExactResult, exact_test
from .montecarlo import MonteCarloResult, monte_carlo_test
from .spaces import IndependentTwoSampleSpace, PairedSwapSpace, StratifiedSpace

#: Spaces at or below this size default to exact enumeration.
DEFAULT_EXACT_LIMIT = 200_000


def two_sample_test(
    group1: Sequence[float],
    group2: Sequence[float],
    alternative: str = "two-sided",
    method: str = "auto",
    draws: int = 10_000,
    seed=None,
    n_shards: int = 1,
    exact_limit: int = DEFAULT_EXACT_LIMIT,
    checkpoint_path: Optional[str] = None,
    resume: bool = False,
):
    """Independent two-sample test on the difference of means.

    Groups may have unequal sizes.  method="auto" enumerates exactly
    when C(N, n1) <= exact_limit and otherwise falls back to Monte
    Carlo with the given draws and seed.
    """
    values = list(group1) + list(group2)
    space = IndependentTwoSampleSpace(values, len(group1))
    if method == "exact" or (method == "auto" and space.size <= exact_limit):
        return exact_test(
            space, alternative, n_shards=n_shards,
            checkpoint_path=checkpoint_path, resume=resume,
        )
    if method in ("auto", "monte-carlo"):
        return monte_carlo_test(space, draws, seed=seed, alternative=alternative)
    raise ValueError("method must be 'auto', 'exact' or 'monte-carlo'")


def paired_test(
    before: Sequence[float],
    after: Sequence[float],
    alternative: str = "two-sided",
    method: str = "auto",
    draws: int = 10_000,
    seed=None,
    n_shards: int = 1,
    exact_limit: int = DEFAULT_EXACT_LIMIT,
):
    """Paired (sign-flip) test on the mean of within-pair differences."""
    if len(before) != len(after):
        raise ValueError("paired test needs equal-length sequences")
    differences = [float(a) - float(b) for a, b in zip(after, before)]
    space = PairedSwapSpace(differences)
    if method == "exact" or (method == "auto" and space.size <= exact_limit):
        return exact_test(space, alternative, n_shards=n_shards)
    if method in ("auto", "monte-carlo"):
        return monte_carlo_test(space, draws, seed=seed, alternative=alternative)
    raise ValueError("method must be 'auto', 'exact' or 'monte-carlo'")


def stratified_test(
    strata: Sequence[Tuple[Sequence[float], Sequence[float]]],
    alternative: str = "two-sided",
    method: str = "auto",
    draws: int = 10_000,
    seed=None,
    n_shards: int = 1,
    exact_limit: int = DEFAULT_EXACT_LIMIT,
):
    """Stratified test: permute labels within each stratum only.

    strata is a sequence of (group1_values, group2_values) pairs, one
    per stratum; group sizes may differ across and within strata.
    """
    space = StratifiedSpace([(list(g1) + list(g2), len(g1)) for g1, g2 in strata])
    if method == "exact" or (method == "auto" and space.size <= exact_limit):
        return exact_test(space, alternative, n_shards=n_shards)
    if method in ("auto", "monte-carlo"):
        return monte_carlo_test(space, draws, seed=seed, alternative=alternative)
    raise ValueError("method must be 'auto', 'exact' or 'monte-carlo'")
