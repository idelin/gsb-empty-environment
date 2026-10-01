"""Numerically stable evaluation of the subsampling amplification formula.

Definition (as given in the task)
---------------------------------
For a base mechanism that is (eps, 0)-DP, Poisson-subsampling with rate q
(``add/remove`` adjacency) yields the amplified privacy parameter

    eps' = log(1 + q * (exp(eps) - 1))

Two regimes are used so that no overflow and no avoidable loss of
relative accuracy can occur:

* eps <= 700 (direct regime):  expm1(700) ~ 1e304 is representable, so
  ``log1p(q * expm1(eps))`` is evaluated directly at full float accuracy.
* eps > 700 (log-space regime):  exp(eps) would overflow float64, and the
  result is recovered from logarithms,

    log_t = log(q) + log(expm1(eps))
          where  log(expm1(eps)) = eps + log1p(-exp(-eps))   (eps huge)
    eps'  = log1p(t),  t = exp(log_t)
          = log1p(exp(log_t))                    if log_t <  log(1e-5)
          = log_t + log1p(exp(-log_t))           otherwise

  Both branches only exponentiate quantities whose result is
  representable, so no overflow can occur.  (In this regime the relative
  accuracy is limited to ~eps * 2^-53 by the float64 representation of
  log(q) itself; that is inherent to float64, not to this rewrite.)

Boundary cases are exact:

    q = 0  ->  eps' = 0.0        (exact)
    q = 1  ->  eps' = eps        (exact, returned directly)
    eps = 0 ->  eps' = 0.0       (exact)

Scope / disclaimer
------------------
* Synthetic parameters only.  A correctly computed number is **not** a
  privacy proof; the formula is only as valid as its assumptions (Poisson
  subsampling, (eps, 0)-DP base mechanism).  See ``assumptions.py``.
"""

from __future__ import annotations

import math

# Branch cut for the two log1p evaluation forms (see module docstring).
_LOG_BRANCH_CUT = math.log(1e-5)

# Below this eps the direct form log1p(q*expm1(eps)) cannot overflow
# (expm1(700) ~ 1e304, q <= 1) and is used at full relative accuracy.
_DIRECT_EPS_LIMIT = 700.0


def validate_params(q: float, eps: float) -> None:
    """Reject parameters outside the domain of the given formula.

    The formula eps' = log(1 + q*(exp(eps)-1)) is stated for a sampling
    probability q in [0, 1] and a privacy parameter eps >= 0.  Anything
    else (NaN, infinities, q outside [0,1], negative eps) means the input
    does not satisfy the sampling model and must be rejected rather than
    silently clamped.
    """
    if isinstance(q, bool) or isinstance(eps, bool):
        raise ValueError("q and eps must be real numbers, not booleans")
    q = float(q)
    eps = float(eps)
    if math.isnan(q) or math.isnan(eps):
        raise ValueError("q and eps must not be NaN")
    if math.isinf(q) or math.isinf(eps):
        raise ValueError("q and eps must be finite")
    if not 0.0 <= q <= 1.0:
        raise ValueError(
            f"q={q!r} is outside [0, 1]; the subsampling formula does not apply"
        )
    if eps < 0.0:
        raise ValueError(
            f"eps={eps!r} is negative; the subsampling formula does not apply"
        )


def log_expm1(eps: float) -> float:
    """Compute log(exp(eps) - 1) stably for eps > 0 (may be -inf for eps == 0).

    For eps >= 1 uses eps + log1p(-exp(-eps)), which never evaluates
    exp(eps) itself and is accurate even for eps far beyond the overflow
    threshold (~709.78).  For 0 < eps < 1, expm1 is accurate directly.
    """
    if eps < 0.0:
        raise ValueError("log_expm1 requires eps >= 0")
    if eps == 0.0:
        return -math.inf
    if eps < 1.0:
        return math.log(math.expm1(eps))
    return eps + math.log1p(-math.exp(-eps))


def eps_prime(q: float, eps: float) -> float:
    """Stable evaluation of eps' = log(1 + q*(exp(eps) - 1)).

    Exact on the boundaries q == 0 (-> 0.0) and q == 1 (-> eps).
    Never overflows for large eps.
    """
    validate_params(q, eps)
    q = float(q)
    eps = float(eps)
    if q == 0.0 or eps == 0.0:
        return 0.0
    if q == 1.0:
        return eps  # log(1 + exp(eps) - 1) == eps, returned exactly

    if eps <= _DIRECT_EPS_LIMIT:
        # Direct regime: expm1(eps) <= ~1e304, q <= 1, no overflow.
        return math.log1p(q * math.expm1(eps))

    # Log-space regime (eps > 700): never forms exp(eps).
    log_t = math.log(q) + log_expm1(eps)
    if log_t < _LOG_BRANCH_CUT:
        # t = q*(exp(eps)-1) is small and representable; exp(log_t) cannot
        # overflow.  It may underflow to 0.0, in which case the true eps'
        # is below the smallest subnormal and 0.0 is the correct float.
        return math.log1p(math.exp(log_t))
    # t >= 1e-5: use log1p(t) = log(t) + log1p(1/t) with log(t) = log_t.
    # exp(-log_t) <= 1e-5, so log1p of it is computed at full accuracy.
    return log_t + math.log1p(math.exp(-log_t))
