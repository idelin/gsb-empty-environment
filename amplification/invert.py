"""Invert the amplification formula: given a target budget, find max q.

Solving  budget = log(1 + q*(exp(eps) - 1))  for q gives the analytic
maximizer

    q* = (exp(budget) - 1) / (exp(eps) - 1)
       = exp( log(expm1(budget)) - log(expm1(eps)) )      (stable form)

clipped to [0, 1] (q is a sampling probability; budget >= eps allows
q = 1, and budget == 0 forces q = 0).

Because a float q* is not a proof, ``max_q_for_budget`` additionally
returns a **certified enclosing interval** [q_lo, q_hi] built with the
outward-rounded interval evaluator:

    eps'_interval(q_lo).hi <= budget   (certified: q_lo is feasible)
    eps'_interval(q_hi).lo >  budget   (certified: q_hi is infeasible)

which guarantees  q_lo <= q* <= q_hi  for the true maximizer q*
(q_hi = 1.0 may also be the domain cap q <= 1).  The result carries the
interval evaluations and residuals as inspectable evidence.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

from . import intervals, stable


@dataclass(frozen=True)
class InversionResult:
    eps: float
    budget: float
    q_analytic: float          # stable float evaluation of q*
    q_lo: float                # certified feasible (interval eval <= budget)
    q_hi: float                # certified infeasible, or domain cap 1.0
    q_hi_is_domain_cap: bool   # True if q_hi = 1.0 is just the cap q <= 1
    eps_at_q_lo: intervals.Interval   # interval eval of eps' at q_lo
    eps_at_q_hi: intervals.Interval   # interval eval of eps' at q_hi
    residual_lo: float         # budget - eps_at_q_lo.hi  (>= 0: feasible)
    residual_hi: float         # budget - eps_at_q_hi.lo  (< 0 unless cap)

    @property
    def bracket_width(self) -> float:
        return self.q_hi - self.q_lo


def _analytic_q(eps: float, budget: float) -> float:
    """Stable float evaluation of q* = expm1(budget)/expm1(eps) in [0, 1]."""
    if budget == 0.0:
        return 0.0
    if budget >= eps:
        return 1.0
    # budget < eps, both > 0: ratio < 1, computed entirely in log-space.
    log_ratio = stable.log_expm1(budget) - stable.log_expm1(eps)
    return math.exp(log_ratio)


def _eval_interval(q: float, eps: float) -> intervals.Interval:
    return intervals.eps_prime(intervals.const(q), intervals.const(eps))


def _certified_feasible(q: float, eps: float, budget: float) -> bool:
    return _eval_interval(q, eps).hi <= budget


def _certified_infeasible(q: float, eps: float, budget: float) -> bool:
    return _eval_interval(q, eps).lo > budget


def _bisect_point(eps: float, budget: float) -> tuple[float, float]:
    """Adjacent floats (lo, hi) with f(lo) <= budget < f(hi), f monotone."""
    lo, hi = 0.0, 1.0
    while True:
        mid = lo + (hi - lo) / 2.0
        if mid == lo or mid == hi:
            return lo, hi
        if stable.eps_prime(mid, eps) <= budget:
            lo = mid
        else:
            hi = mid


def _bisect_predicate(lo: float, hi: float, pred) -> tuple[float, float]:
    """Adjacent floats (lo, hi) with pred(lo) true and pred(hi) false,
    for a monotone-down predicate on floats."""
    while True:
        mid = lo + (hi - lo) / 2.0
        if mid == lo or mid == hi:
            return lo, hi
        if pred(mid):
            lo = mid
        else:
            hi = mid


def _scan_certified_lo(eps: float, budget: float, start: float) -> float:
    """Largest certified-feasible float at or below ``start``.

    Geometric search downward (the certified point may lie many ulps below
    the pointwise root in extreme regimes), then float bisection between
    the bracketing points, using monotonicity of the certificate.
    """
    pred = lambda q: _certified_feasible(q, eps, budget)
    if pred(start):
        return start
    # Geometric expansion of the distance from ``start`` toward 0.
    dist = math.nextafter(start, 0.0) - start  # negative: one ulp
    while True:
        q = start + dist
        if q <= 0.0:
            return 0.0  # eps'(0) = 0 <= budget always holds
        if pred(q):
            lo, _ = _bisect_predicate(q, start + dist / 2.0, pred)
            return lo
        dist *= 2.0


def _scan_certified_hi(eps: float, budget: float, start: float) -> tuple[float, bool]:
    """Smallest certified-infeasible float at or above ``start``.

    Returns (q_hi, is_domain_cap); falls back to the domain cap q = 1 when
    no float below 1 is certifiably infeasible (budget within a few ulps of
    eps), which still encloses q* because q <= 1 by definition.
    """
    pred = lambda q: _certified_infeasible(q, eps, budget)
    if pred(start):
        return start, False
    dist = math.nextafter(start, 1.0) - start  # positive: one ulp
    while True:
        q = start + dist
        if q >= 1.0:
            return 1.0, True
        if pred(q):
            # Monotone-up predicate: certified-infeasible stays infeasible.
            _, hi = _bisect_predicate_up(start + dist / 2.0, q, pred)
            return hi, False
        dist *= 2.0


def _bisect_predicate_up(lo: float, hi: float, pred) -> tuple[float, float]:
    """Adjacent floats (lo, hi) with pred(lo) false and pred(hi) true,
    for a monotone-up predicate on floats."""
    while True:
        mid = lo + (hi - lo) / 2.0
        if mid == lo or mid == hi:
            return lo, hi
        if pred(mid):
            hi = mid
        else:
            lo = mid


def max_q_for_budget(eps: float, budget: float) -> InversionResult:
    """Maximal sampling rate q with eps'(q) <= budget, plus a certificate."""
    stable.validate_params(0.0, eps)          # validates eps
    if math.isnan(budget) or math.isinf(budget) or budget < 0.0:
        raise ValueError(f"budget={budget!r} must be a finite value >= 0")
    eps = float(eps)
    budget = float(budget)

    q_star = _analytic_q(eps, budget)

    if budget == 0.0:
        # eps'(q) = 0 iff q = 0 (for eps > 0); the certificate is exact.
        lo = hi = 0.0
        cap = False
    elif stable.eps_prime(1.0, eps) <= budget:
        # Full sampling fits the budget; q* = 1 (domain cap).
        lo = _scan_certified_lo(eps, budget, 1.0)
        hi, cap = 1.0, True
    else:
        lo_pt, hi_pt = _bisect_point(eps, budget)
        lo = _scan_certified_lo(eps, budget, lo_pt)
        hi, cap = _scan_certified_hi(eps, budget, hi_pt)

    eps_lo_iv = _eval_interval(lo, eps)
    eps_hi_iv = _eval_interval(hi, eps)
    return InversionResult(
        eps=eps,
        budget=budget,
        q_analytic=q_star,
        q_lo=lo,
        q_hi=hi,
        q_hi_is_domain_cap=cap,
        eps_at_q_lo=eps_lo_iv,
        eps_at_q_hi=eps_hi_iv,
        residual_lo=budget - eps_lo_iv.hi,
        residual_hi=budget - eps_hi_iv.lo,
    )
