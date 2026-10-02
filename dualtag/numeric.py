"""Independent finite-difference references used to validate the AD results.

These share no code with the dual-number machinery, so the test-suite
comparisons are genuine cross-checks.  ``bisect_root`` additionally gives
an independent (non-Newton) root finder for validating implicit solves.
"""
from __future__ import annotations


def fd_derivative(f, x, h=1e-5):
    """Central first derivative of a univariate function."""
    return (f(x + h) - f(x - h)) / (2.0 * h)


def fd_second(f, x, h=1e-3):
    """Central second derivative of a univariate function."""
    return (f(x + h) - 2.0 * f(x) + f(x - h)) / (h * h)


def fd_mixed(fn, x, i, j, h=1e-4):
    """Four-point mixed partial of a scalar function of a vector."""
    def at(di, dj):
        xx = list(x)
        xx[i] += di
        xx[j] += dj
        return fn(xx)
    return (at(h, h) - at(h, -h) - at(-h, h) + at(-h, -h)) / (4.0 * h * h)


def fd_jacobian(fn, x, h=1e-6):
    """Central-difference Jacobian of a vector function."""
    x = list(x)
    base = fn(x)
    m = len(base) if isinstance(base, (list, tuple)) else 1
    J = [[0.0] * len(x) for _ in range(m)]
    for j in range(len(x)):
        xp = list(x)
        xm = list(x)
        xp[j] += h
        xm[j] -= h
        fp = fn(xp)
        fm = fn(xm)
        if not isinstance(fp, (list, tuple)):
            fp, fm = [fp], [fm]
        for i in range(m):
            J[i][j] = (fp[i] - fm[i]) / (2.0 * h)
    return J


def bisect_root(g, lo, hi, *, tol=1e-14, max_iter=200):
    """Bisection root finder -- independent of the Newton solver."""
    flo = g(lo)
    fhi = g(hi)
    if flo == 0.0:
        return lo
    if fhi == 0.0:
        return hi
    if flo * fhi > 0:
        raise ValueError("bisection interval does not bracket a root")
    for _ in range(max_iter):
        mid = 0.5 * (lo + hi)
        fm = g(mid)
        if abs(fm) <= tol or (hi - lo) <= tol:
            return mid
        if flo * fm <= 0:
            hi, fhi = mid, fm
        else:
            lo, flo = mid, fm
    return 0.5 * (lo + hi)
