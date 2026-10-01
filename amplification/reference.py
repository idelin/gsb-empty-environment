"""Independent high-precision series reference (standard library ``decimal``).

This module recomputes

    eps' = log(1 + q*(exp(eps) - 1))      and      q* = expm1(budget)/expm1(eps)

with user-selected precision (default 80 significant decimal digits) using
only power-series evaluations implemented here:

  * expm1 via its Taylor series with argument halving
    (expm1(2y) = 2*expm1(y) + expm1(y)^2),
  * ln via the atanh series  ln(z) = 2 * sum_k w^(2k+1)/(2k+1),
    w = (z-1)/(z+1), with range reduction by powers of two,
  * ln(2) itself via the same atanh series at z = 2.

It shares no code path with the float implementation in ``stable.py`` and
never calls ``Decimal.exp``/``Decimal.ln`` or libm, so agreement between
the two is meaningful cross-validation, including at the boundaries
(q -> 0, q = 1, eps huge) where naive float code would overflow.

NOTE: this is a *numerical* reference for synthetic parameters.  Matching
numbers are evidence of computational correctness, not a privacy proof.
"""

from __future__ import annotations

from decimal import Decimal, localcontext

DEFAULT_PREC = 80


def _expm1_series(x: Decimal, prec: int) -> Decimal:
    """expm1(x) = exp(x) - 1 by Taylor series with argument halving."""
    with localcontext() as ctx:
        ctx.prec = prec + 10
        reductions = 0
        y = x
        bound = Decimal(1) / 8
        while abs(y) > bound:
            y /= 2
            reductions += 1
        # Taylor sum for expm1(y), |y| <= 1/8 converges very fast.
        term = y
        total = y
        n = 1
        while True:
            n += 1
            term = term * y / n
            if term.is_zero() or abs(term) < Decimal(10) ** (-(prec + 8)):
                total += term
                break
            total += term
        # Square back: expm1(2y) = 2*e + e^2 with e = expm1(y).
        for _ in range(reductions):
            total = 2 * total + total * total
        ctx.prec = prec
        return +total


def _ln_series(z: Decimal, prec: int) -> Decimal:
    """ln(z) for z > 0 via atanh series with power-of-two range reduction."""
    if z <= 0:
        raise ValueError("ln series requires z > 0")
    with localcontext() as ctx:
        ctx.prec = prec + 10
        ln2 = _ln2(prec + 10)
        m = 0
        hi = Decimal("1.5")
        lo = Decimal("0.75")
        while z > hi:
            z /= 2
            m += 1
        while z < lo:
            z *= 2
            m -= 1
        w = (z - 1) / (z + 1)
        w2 = w * w
        term = w
        total = w
        k = 0
        while True:
            k += 1
            term *= w2
            add = term / (2 * k + 1)
            if add.is_zero() or abs(add) < Decimal(10) ** (-(prec + 8)):
                total += add
                break
            total += add
        result = 2 * total + m * ln2
        ctx.prec = prec
        return +result


def _ln2(prec: int) -> Decimal:
    """ln(2) = 2*atanh(1/3) via the same series (no shared constants)."""
    with localcontext() as ctx:
        ctx.prec = prec + 5
        w = Decimal(1) / 3
        w2 = w * w
        term = w
        total = w
        k = 0
        while True:
            k += 1
            term *= w2
            add = term / (2 * k + 1)
            if add.is_zero() or abs(add) < Decimal(10) ** (-(prec + 3)):
                total += add
                break
            total += add
        return 2 * total


def reference_eps_prime(q, eps, prec: int = DEFAULT_PREC) -> Decimal:
    """High-precision eps' = ln(1 + q*(exp(eps) - 1)) from string/float inputs."""
    qd = Decimal(str(q))
    ed = Decimal(str(eps))
    if not (Decimal(0) <= qd <= Decimal(1)):
        raise ValueError("q outside [0, 1]")
    if ed < 0:
        raise ValueError("eps must be >= 0")
    if qd == 0 or ed == 0:
        return Decimal(0)
    if qd == 1:
        return ed  # exact boundary: log(1 + exp(eps) - 1) == eps
    with localcontext() as ctx:
        ctx.prec = prec + 10
        t = qd * _expm1_series(ed, prec + 10)
        result = _ln_series(1 + t, prec + 10)
        ctx.prec = prec
        return +result


def reference_max_q(eps, budget, prec: int = DEFAULT_PREC) -> Decimal:
    """High-precision q* = expm1(budget)/expm1(eps), clipped to [0, 1]."""
    ed = Decimal(str(eps))
    bd = Decimal(str(budget))
    if ed < 0 or bd < 0:
        raise ValueError("eps and budget must be >= 0")
    if bd == 0:
        return Decimal(0)
    if bd >= ed:
        return Decimal(1)
    with localcontext() as ctx:
        ctx.prec = prec + 10
        num = _expm1_series(bd, prec + 10)
        den = _expm1_series(ed, prec + 10)
        result = num / den
        ctx.prec = prec
        return +result
