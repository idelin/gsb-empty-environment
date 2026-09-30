"""Numerically stable scalar evaluation of eps' = log(1 + q*(exp(eps) - 1)).

Two algebraically identical forms are used:

  (A) log1p(q * expm1(eps))                 -- accurate for moderate eps
  (B) eps + log1p((1 - q) * expm1(-eps))    -- safe for arbitrarily large eps

Form (B) follows from the identity

    1 + q*(e^eps - 1) = e^eps * (q + (1 - q)*e^-eps)

so log(1 + q*(e^eps - 1)) = eps + log(q + (1 - q)*e^-eps).

Form (B) never evaluates exp(eps); expm1(-eps) lies in (-1, 0] and
underflows gracefully to exactly -1.0 for huge eps, so the computation
cannot overflow (no "compute exp, overflow, then clip" anti-pattern).
The sum q + (1 - q)*e^-eps is a sum of nonnegative terms, so it cannot
collapse to zero even when both q and e^-eps are tiny (unlike the
algebraically equivalent log1p((1 - q)*expm1(-eps)), whose argument can
round to exactly -1).

Exact boundaries, returned exactly:
  q = 0   -> 0
  eps = 0 -> 0
  q = 1   -> eps
"""

from __future__ import annotations

import math

from .validation import SamplingModel, validate_subsample_applicability

# expm1(x) overflows to inf for x >~ 709.78; stay well below that.
_OVERFLOW_GUARD = 700.0


def subsampled_epsilon(
    eps: float,
    q: float,
    *,
    model: SamplingModel = SamplingModel.POISSON,
    base_delta: float = 0.0,
) -> float:
    """Stable float evaluation of log(1 + q*(exp(eps) - 1)).

    The applicability conditions of the Poisson amplification formula are
    validated first; invalid inputs raise ApplicabilityError.
    """
    eps, q = validate_subsample_applicability(
        eps, q, model=model, base_delta=base_delta
    )
    if q == 0.0 or eps == 0.0:
        return 0.0
    if q == 1.0:
        return eps
    if eps <= _OVERFLOW_GUARD:
        return math.log1p(q * math.expm1(eps))
    return eps + math.log(q + (1.0 - q) * math.exp(-eps))


def log_expm1(x: float) -> float:
    """log(exp(x) - 1) for x > 0, without overflow for large x.

    Uses log(e^x - 1) = x + log1p(-e^-x) for large x, where e^-x
    underflows gracefully to 0.0 and the result degrades to exactly x,
    which is the correctly rounded answer in that regime.
    """
    if not x > 0.0:
        raise ValueError(f"log_expm1 requires x > 0, got {x!r}")
    if x <= 40.0:
        return math.log(math.expm1(x))
    return x + math.log1p(-math.exp(-x))
