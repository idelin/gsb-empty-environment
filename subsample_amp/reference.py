"""Independent high-precision series reference (verification only).

This module deliberately does NOT use ``Decimal.exp`` / ``Decimal.ln``:
exp and ln are computed from their defining power series with argument
reduction, so the reference shares no code path with the interval
evaluator it is used to cross-check.

  * exp(x): range reduction x = k*ln2 + r, Taylor series for e^r, exact
    rescaling by 2^k.  ln2 itself is computed from the atanh series.
  * ln(y): range reduction y = m * 2^e with m in [2/3, 4/3], then
    ln(m) = 2*atanh((m-1)/(m+1)) with |(m-1)/(m+1)| <= 1/5.

This is a numerical cross-check of arithmetic only -- not a privacy proof.
"""

from __future__ import annotations

from decimal import ROUND_HALF_EVEN, Context, Decimal, localcontext

_GUARD = 25
_ONE = Decimal(1)
_TWO = Decimal(2)
_LN2_CACHE: dict[int, Decimal] = {}


def _ln2(prec: int) -> Decimal:
    """ln 2 = 2 * atanh(1/3) via its Taylor series."""
    if prec in _LN2_CACHE:
        return _LN2_CACHE[prec]
    with localcontext(Context(prec=prec + _GUARD)) as ctx:
        term = _ONE / 3
        total = term
        t2 = term * term
        n = 1
        eps = _ONE.scaleb(-(prec + _GUARD))
        while True:
            term *= t2
            n += 2
            add = term / n
            total += add
            if abs(add) < eps:
                break
        result = +(_TWO * total)  # round to working precision
    _LN2_CACHE[prec] = result
    return result


def series_exp(x: Decimal, prec: int = 80) -> Decimal:
    """exp(x) from the Taylor series with argument reduction by ln 2."""
    x = Decimal(x)
    if x == 0:
        return _ONE
    if x < 0:
        # copy_negate avoids rounding to the default 28-digit context.
        return Context(prec=prec).divide(_ONE, series_exp(x.copy_negate(), prec))
    with localcontext(Context(prec=prec + _GUARD)) as ctx:
        ln2 = _ln2(prec)
        k = int((x / ln2).to_integral_value(rounding=ROUND_HALF_EVEN))
        r = x - k * ln2  # |r| <= ln2/2 < 0.35
        term = _ONE
        total = _ONE
        n = 1
        eps = _ONE.scaleb(-(prec + _GUARD))
        while True:
            term = term * r / n
            total += term
            if abs(term) < eps:
                break
            n += 1
        result = total * (_TWO ** k)  # 2**k is exact in Decimal
    return Context(prec=prec).plus(result)


def series_ln(y: Decimal, prec: int = 80) -> Decimal:
    """ln(y) for y > 0 via atanh series after 2^e range reduction."""
    y = Decimal(y)
    if y <= 0:
        raise ValueError(f"series_ln requires y > 0, got {y!r}")
    with localcontext(Context(prec=prec + _GUARD)) as ctx:
        ln2 = _ln2(prec)
        lo = _TWO / 3
        hi = Decimal(4) / 3
        e = 0
        m = y
        while m > hi:
            m /= 2
            e += 1
        while m < lo:
            m *= 2
            e -= 1
        t = (m - 1) / (m + 1)  # |t| <= 1/5, series converges quickly
        t2 = t * t
        term = t
        total = t
        n = 1
        eps = _ONE.scaleb(-(prec + _GUARD))
        while True:
            term *= t2
            n += 2
            add = term / n
            total += add
            if abs(add) < eps:
                break
        result = e * ln2 + _TWO * total
    return Context(prec=prec).plus(result)


def reference_amp(eps: float, q: float, prec: int = 80) -> Decimal:
    """High-precision reference for log(1 + q*(exp(eps) - 1)).

    Floats are converted to their exact binary Decimal values, matching
    the interval evaluator's semantics.  Exact boundaries are preserved.
    """
    eps_d = Decimal(eps)
    q_d = Decimal(q)
    if q_d == 0 or eps_d == 0:
        return Decimal(0)
    if q_d == 1:
        return eps_d
    with localcontext(Context(prec=prec + _GUARD)):
        inner = _ONE + q_d * (series_exp(eps_d, prec) - _ONE)
        result = series_ln(inner, prec)
    return result
