"""Interval arithmetic with outward rounding for the amplification DAG.

Each elementary operation evaluates its (monotone) float kernel at the
interval endpoints and then widens each endpoint by one ulp via
``math.nextafter``.  This gives rigorous containment under the standard
assumption that the platform libm (``log``, ``exp``, ``log1p``, ``expm1``)
has error below 1 ulp; the extra ulp absorbs the rounding of the kernel
itself.  All quantities here are non-negative, which keeps ``mul`` simple.

The amplified-eps' node is evaluated *compositionally* (log -> add -> exp
-> log1p) so that outward rounding is applied at every elementary step,
not just once at the end of a composite formula.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

from . import stable


def _down(x: float) -> float:
    return math.nextafter(x, -math.inf)


def _up(x: float) -> float:
    return math.nextafter(x, math.inf)


def _down_nonneg(x: float) -> float:
    """Widen downward but never below 0 (for provably non-negative values)."""
    if x <= 0.0:
        return 0.0
    return _down(x)


@dataclass(frozen=True)
class Interval:
    lo: float
    hi: float

    def __post_init__(self) -> None:
        if not (self.lo <= self.hi):
            raise ValueError(f"empty interval: lo={self.lo!r} > hi={self.hi!r}")

    @property
    def width(self) -> float:
        return self.hi - self.lo

    def contains(self, x: float) -> bool:
        return self.lo <= x <= self.hi

    def __repr__(self) -> str:  # compact, readable
        return f"[{self.lo:.17g}, {self.hi:.17g}]"


def const(x: float) -> Interval:
    """Point interval for an exactly representable float."""
    if math.isnan(x):
        raise ValueError("NaN is not a valid interval bound")
    return Interval(x, x)


def hull(a: Interval, b: Interval) -> Interval:
    return Interval(min(a.lo, b.lo), max(a.hi, b.hi))


def add(a: Interval, b: Interval) -> Interval:
    return Interval(_down(a.lo + b.lo), _up(a.hi + b.hi))


def neg(a: Interval) -> Interval:
    # Float negation is exact.
    return Interval(-a.hi, -a.lo)


def mul_nonneg(a: Interval, b: Interval) -> Interval:
    """Product of two non-negative intervals."""
    if a.lo < 0.0 or b.lo < 0.0:
        raise ValueError("mul_nonneg requires non-negative operands")
    return Interval(_down_nonneg(a.lo * b.lo), _up(a.hi * b.hi))


def exp(a: Interval) -> Interval:
    def _exp_point(x: float) -> float:
        if x == -math.inf:
            return 0.0
        if x == math.inf:
            return math.inf
        try:
            return math.exp(x)
        except OverflowError:
            return math.inf
    return Interval(_down_nonneg(_exp_point(a.lo)), _up(_exp_point(a.hi)))


def log(a: Interval) -> Interval:
    """Natural log on [lo, hi] with lo >= 0; log(0) maps to -inf."""
    if a.lo < 0.0:
        raise ValueError("log of a negative interval")
    lo = -math.inf if a.lo == 0.0 else _down(math.log(a.lo))
    hi = -math.inf if a.hi == 0.0 else _up(math.log(a.hi))
    return Interval(lo, hi)


def log1p(a: Interval) -> Interval:
    if a.lo < 0.0:
        raise ValueError("log1p domain here is non-negative")
    return Interval(_down_nonneg(math.log1p(a.lo)), _up(math.log1p(a.hi)))


def expm1(a: Interval) -> Interval:
    """expm1 on a non-negative interval; overflows to +inf above ~709.78."""
    if a.lo < 0.0:
        raise ValueError("expm1 domain here is non-negative")

    def _point(x: float) -> float:
        try:
            return math.expm1(x)
        except OverflowError:
            return math.inf
    return Interval(_down_nonneg(_point(a.lo)), _up(_point(a.hi)))


def log_expm1(a: Interval) -> Interval:
    """log(exp(x) - 1) for x >= 0, via the stable scalar kernel."""
    lo = -math.inf if a.lo == 0.0 else _down(stable.log_expm1(a.lo))
    hi = -math.inf if a.hi == 0.0 else _up(stable.log_expm1(a.hi))
    return Interval(lo, hi)


def eps_prime(q: Interval, eps: Interval) -> Interval:
    """Outward-rounded interval for log(1 + q*(exp(eps) - 1)).

    Compositional evaluation of the stable rewrite

        log_t = log(q) + log(expm1(eps))
        eps'  = log1p(exp(log_t))            (log_t < log(1e-5))
              = log_t + log1p(exp(-log_t))   (otherwise)

    with one-ulp outward rounding at every elementary step.  Handles
    q = 0 and eps = 0 through -inf arithmetic (exp(-inf) = 0), so the
    boundary intervals come out as [0, tiny] -- containing the true 0.
    """
    if q.lo < 0.0 or q.hi > 1.0:
        raise ValueError("q interval must lie inside [0, 1]")
    if eps.lo < 0.0:
        raise ValueError("eps interval must be non-negative")

    limit = stable._DIRECT_EPS_LIMIT

    def direct(q_iv: Interval, e_iv: Interval) -> Interval:
        # eps <= 700: expm1 cannot overflow; full relative accuracy.
        return log1p(mul_nonneg(q_iv, expm1(e_iv)))

    def logspace(q_iv: Interval, e_iv: Interval) -> Interval:
        log_t = add(log(q_iv), log_expm1(e_iv))
        cut = stable._LOG_BRANCH_CUT

        def small_branch(lt: Interval) -> Interval:
            return log1p(exp(lt))

        def large_branch(lt: Interval) -> Interval:
            return add(lt, log1p(exp(neg(lt))))

        # Branches are evaluated lazily: the large branch is only formed
        # where log_t is bounded below, so -inf (from q == 0) never
        # reaches exp() in a form that would produce NaN.
        if log_t.hi < cut:
            return small_branch(log_t)
        if log_t.lo >= cut:
            return large_branch(log_t)
        # Straddling the branch cut: split at the cut and hull the parts;
        # both forms are mathematically equal, so the hull is a valid
        # (slightly wider) enclosure.
        lo_part = small_branch(Interval(log_t.lo, cut))
        hi_part = large_branch(Interval(cut, log_t.hi))
        return hull(lo_part, hi_part)

    if eps.hi <= limit:
        return direct(q, eps)
    if eps.lo >= limit:
        return logspace(q, eps)
    # Straddling the regime boundary: both regimes are valid enclosures on
    # their sub-domains, so the hull encloses the whole interval.
    return hull(
        direct(q, Interval(eps.lo, limit)),
        logspace(q, Interval(limit, eps.hi)),
    )
