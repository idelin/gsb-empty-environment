"""Exact real-root isolation for rational polynomials.

Rational roots are found exactly via the rational-root theorem (after
clearing denominators to a primitive integer polynomial).  Irrational roots
are isolated into disjoint open intervals with rational endpoints using
Sturm's theorem and bisection.  A bisection *budget* bounds the total work;
intervals that are not resolved when the budget is exhausted are reported as
``unknown`` clusters instead of being guessed.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .poly import (
    Poly,
    cauchy_bound,
    integer_divisors,
    sign_variations,
    sturm_sequence,
)

DEFAULT_BUDGET = 256
MAX_RATIONAL_CANDIDATES = 4096


@dataclass(frozen=True)
class RationalRoot:
    """An exact rational root together with its multiplicity."""

    value: Fraction
    multiplicity: int


@dataclass(frozen=True)
class IsolatedRoot:
    """An open interval (lo, hi) containing exactly one (irrational) root."""

    lo: Fraction
    hi: Fraction


@dataclass(frozen=True)
class UnknownCluster:
    """Interval holding ``count`` roots that the budget could not separate."""

    lo: Fraction
    hi: Fraction
    count: int


@dataclass
class RootAnalysis:
    """Complete classification of the real roots of a polynomial."""

    rational: list[RationalRoot] = field(default_factory=list)
    isolated: list[IsolatedRoot] = field(default_factory=list)
    unknown: list[UnknownCluster] = field(default_factory=list)
    exhausted: bool = False

    def covers(self, point: Fraction) -> bool:
        """True if ``point`` is a known rational root of the polynomial."""
        return any(r.value == point for r in self.rational)


def _rational_root_candidates(poly: Poly) -> list[Fraction]:
    ints = poly.to_int_tuple()
    if ints == (0,):
        return []
    const, lead = ints[0], ints[-1]
    if const == 0:
        return [Fraction(0)]
    out: list[Fraction] = []
    for p in integer_divisors(const):
        for q in integer_divisors(lead):
            out.append(Fraction(p, q))
            out.append(Fraction(-p, q))
    return out


def _isolate_interval(chain, lo: Fraction, hi: Fraction, count: int,
                      budget: list[int], isolated, unknown) -> None:
    """Bisect (lo, hi) until each sub-interval holds at most one root."""
    stack = [(lo, hi, count)]
    while stack:
        a, b, n = stack.pop()
        if n == 0:
            continue
        if n == 1:
            isolated.append(IsolatedRoot(a, b))
            continue
        if budget[0] <= 0:
            unknown.append(UnknownCluster(a, b, n))
            continue
        budget[0] -= 1
        mid = (a + b) / 2
        v_mid = sign_variations(chain, mid)
        v_a = sign_variations(chain, a)
        v_b = sign_variations(chain, b)
        left, right = v_a - v_mid, v_mid - v_b
        if chain[0].eval(mid) == 0:
            # The midpoint is itself an exact rational root; the caller strips
            # all rational roots first, so this should not happen, but guard
            # anyway by nudging: treat it as belonging to the right half.
            right += 1
            left -= 1
        if left > 0:
            stack.append((a, mid, left))
        if right > 0:
            stack.append((mid, b, right))


def analyze_roots(poly: Poly, budget: int = DEFAULT_BUDGET) -> RootAnalysis:
    """Classify every real root of ``poly`` (exact rational + isolated).

    Steps, each valid on the stated domain:
      1. Rational-root theorem on the primitive integer form (exact).
      2. Factor out each rational root with multiplicity (exact division).
      3. Sturm bisection of the remaining square-free-agnostic factor inside
         the Cauchy bound, spending at most ``budget`` bisections.
    """
    result = RootAnalysis()
    if poly.is_zero():
        raise ValueError("the zero polynomial vanishes everywhere")
    if poly.degree == 0:
        return result

    remaining = poly
    for candidate in _rational_root_candidates(poly):
        if len(result.rational) >= MAX_RATIONAL_CANDIDATES:
            break
        if remaining.degree == 0:
            break
        if remaining.eval(candidate) != 0:
            continue
        remaining, mult = remaining.factor_linear(candidate)
        result.rational.append(RationalRoot(candidate, mult))

    if remaining.degree == 0:
        result.rational.sort(key=lambda r: r.value)
        return result

    chain = sturm_sequence(remaining)
    bound = cauchy_bound(remaining)
    lo, hi = Fraction(-bound), Fraction(bound)
    total = sign_variations(chain, lo) - sign_variations(chain, hi)
    spend = [budget]
    _isolate_interval(chain, lo, hi, total, spend, result.isolated, result.unknown)
    result.isolated.sort(key=lambda r: r.lo)
    result.unknown.sort(key=lambda r: r.lo)
    result.rational.sort(key=lambda r: r.value)
    result.exhausted = bool(result.unknown)
    return result


def refine_root(chain, interval: IsolatedRoot, budget: list[int],
                target_width: Fraction = Fraction(1)) -> IsolatedRoot:
    """Narrow a single-root isolating interval, spending shared budget.

    Refinement stops once the interval is at most ``target_width`` wide;
    callers that need separation from other roots keep refining explicitly.
    """
    lo, hi = interval.lo, interval.hi
    while budget[0] > 0 and hi - lo > target_width:
        budget[0] -= 1
        mid = (lo + hi) / 2
        if chain[0].eval(mid) == 0:
            return IsolatedRoot(mid, mid)
        if sign_variations(chain, lo) - sign_variations(chain, mid) == 1:
            hi = mid
        else:
            lo = mid
    return IsolatedRoot(lo, hi)


def intervals_overlap(a_lo, a_hi, b_lo, b_hi) -> bool:
    """Open-interval overlap test on rational endpoints."""
    return a_lo < b_hi and b_lo < a_hi
