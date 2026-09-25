"""Public conservative remainder bounds for nonlinear DAG steps.

All formulas are standard, publicly documented affine-arithmetic bounds:

* Multiplication (Stolfi & de Figueiredo, affine arithmetic):
  the quadratic term ra*rb of two affine forms is bounded by the product of
  their radii and folded into one fresh noise symbol.

* Reciprocal 1/x on [l, u] with 0 not in [l, u]:
  Chebyshev (min-max) affine envelope  alpha*x + zeta +- delta.
  The deviation  g(x) = 1/x - alpha*x  satisfies  g'(x) = -1/x^2 - alpha,
  whose only zeros are x = +-sqrt(l*u); hence extrema of g on [l, u] are
  attained at l, u or +-sqrt(l*u), and the envelope below is exact min-max.

* Piecewise-linear f on [l, u]:
  f - alpha*x is again piecewise linear with kinks only at the breakpoints,
  so its extrema on [l, u] are attained at the endpoints *and* the interior
  breakpoints.  We evaluate all of them (never just the endpoints), giving an
  exact min-max envelope; when no breakpoint lies inside [l, u] the remainder
  delta is exactly 0 and the step is a plain linear map.
"""

import math


class DivisionAcrossZero(Exception):
    """Raised when a divisor/reciprocal interval contains 0."""


def multiplication_remainder(radius_a, radius_b):
    """Conservative bound on the product of the two noisy parts."""
    return radius_a * radius_b


def affine_envelope(l, u, f, extra_points=()):
    """Min-max affine envelope alpha*x + zeta +- delta of f on [l, u].

    Valid whenever the deviation f(x) - alpha*x attains its extrema at l, u
    or one of ``extra_points`` (true for the cases documented above).
    Returns (alpha, zeta, delta) with delta >= 0.
    """
    if u < l:
        raise ValueError(f"empty interval [{l}, {u}]")
    fl, fu = f(l), f(u)
    if u == l:
        return 0.0, fl, 0.0
    alpha = (fu - fl) / (u - l)
    points = [l, u]
    points.extend(p for p in extra_points if l < p < u)
    deviations = [f(p) - alpha * p for p in points]
    lo, hi = min(deviations), max(deviations)
    return alpha, (lo + hi) / 2.0, (hi - lo) / 2.0


def reciprocal_envelope(l, u):
    """Chebyshev envelope of 1/x on [l, u]; raises if 0 in [l, u]."""
    if l <= 0.0 <= u:
        raise DivisionAcrossZero(
            f"reciprocal/division interval [{l}, {u}] contains 0"
        )
    critical = []
    if l * u > 0.0:
        root = math.sqrt(l * u)
        critical.extend((root, -root))
    return affine_envelope(l, u, lambda x: 1.0 / x, critical)


def piecewise_linear_eval(breakpoints, values, left_slope, right_slope, x):
    """Evaluate a piecewise-linear function with linear extrapolation tails.

    Sizes and indices are explicit: len(values) must equal len(breakpoints).
    """
    n = len(breakpoints)
    if n == 0:
        raise ValueError("need at least one breakpoint")
    if len(values) != n:
        raise ValueError(
            f"explicit size mismatch: {n} breakpoints vs {len(values)} values"
        )
    for i in range(n - 1):
        if not breakpoints[i] < breakpoints[i + 1]:
            raise ValueError("breakpoints must be strictly increasing")
    if x <= breakpoints[0]:
        return values[0] + left_slope * (x - breakpoints[0])
    if x >= breakpoints[-1]:
        return values[-1] + right_slope * (x - breakpoints[-1])
    for i in range(n - 1):
        if breakpoints[i] <= x <= breakpoints[i + 1]:
            span = breakpoints[i + 1] - breakpoints[i]
            t = (x - breakpoints[i]) / span
            return values[i] + t * (values[i + 1] - values[i])
    raise AssertionError("unreachable")


def piecewise_linear_envelope(breakpoints, values, left_slope, right_slope, l, u):
    """Exact min-max affine envelope of a piecewise-linear f on [l, u].

    Interior breakpoints are included as candidate extrema, so a kink inside
    the interval is accounted for (split/envelope), never endpoint-only.
    """
    def f(x):
        return piecewise_linear_eval(breakpoints, values, left_slope, right_slope, x)

    interior = [b for b in breakpoints if l < b < u]
    return affine_envelope(l, u, f, interior)
