"""Exact bootstrap distribution by full enumeration (small samples).

For an iid or paired plan with n units, every multiset resample corresponds
to a count vector c with sum(c) = n and probability multinomial(n; c) / n**n.
Enumerating all C(2n-1, n) multisets yields the *exact* bootstrap
distribution — no Monte Carlo error.  Weights are kept as exact Fractions.

Assumption: the statistic is permutation-symmetric (a function of the
multiset of resampled units), which holds for means, variances, quantiles,
paired mean differences, etc.
"""
from __future__ import annotations

import itertools
import math
from dataclasses import dataclass
from fractions import Fraction

from .errors import EnumerationTooLargeError, UnsupportedMethodError
from .quantile import weighted_quantile_inverse_cdf


@dataclass(frozen=True)
class ExactResult:
    """An exact interval (kind == "exact"), labelled separately from Monte
    Carlo estimates."""

    method: str
    kind: str
    plan: str
    estimate: float
    lower: float
    upper: float
    alpha: float
    degenerate: bool
    support_size: int
    n_units: int

    def __str__(self) -> str:
        deg = ", DEGENERATE (point interval)" if self.degenerate else ""
        return (
            f"[{self.kind}] {self.method} {1 - self.alpha:.0%} CI "
            f"({self.plan} plan): ({self.lower:.6g}, {self.upper:.6g})"
            f" | estimate={self.estimate:.6g} n_units={self.n_units}"
            f" support={self.support_size}{deg}"
        )


def _multinomial_weight(counts, n: int) -> Fraction:
    denom = 1
    for c in counts.values():
        denom *= math.factorial(c)
    return Fraction(math.factorial(n), denom * n ** n)


def bootstrap_exact(plan, statistic, *, alpha: float = 0.05,
                    max_support: int = 200_000) -> ExactResult:
    """Exact percentile interval from the fully enumerated bootstrap
    distribution (iid and paired plans only).

    Quantiles use the inverse-CDF definition on the exact discrete
    distribution (see quantile.weighted_quantile_inverse_cdf).
    """
    if plan.kind not in ("iid", "paired"):
        raise UnsupportedMethodError(
            f"exact enumeration is implemented for iid and paired plans, "
            f"not {plan.kind!r}")
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha must be in (0, 1), got {alpha!r}")

    units = plan.units
    n = len(units)
    n_multisets = math.comb(2 * n - 1, n)
    if n_multisets > max_support:
        raise EnumerationTooLargeError(
            f"exact enumeration needs {n_multisets} multisets "
            f"(budget max_support={max_support}); use the Monte Carlo "
            "engine or raise the budget explicitly")

    observed = float(statistic(plan.full_sample()))

    support = {}
    for combo in itertools.combinations_with_replacement(range(n), n):
        counts = {}
        for idx in combo:
            counts[idx] = counts.get(idx, 0) + 1
        payload = []
        for idx, count in counts.items():
            payload.extend([units[idx]] * count)
        value = float(statistic(payload))
        support[value] = support.get(value, Fraction(0)) + _multinomial_weight(counts, n)

    items = sorted(support.items())
    degenerate = len(items) == 1
    lower = weighted_quantile_inverse_cdf(items, alpha / 2.0)
    upper = weighted_quantile_inverse_cdf(items, 1.0 - alpha / 2.0)

    return ExactResult(
        method="percentile",
        kind="exact",
        plan=plan.kind,
        estimate=observed,
        lower=lower,
        upper=upper,
        alpha=alpha,
        degenerate=degenerate,
        support_size=len(items),
        n_units=n,
    )
