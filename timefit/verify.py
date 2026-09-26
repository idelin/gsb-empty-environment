"""Independent cross-check: exact rational vertex enumeration.

Re-derives the same problem (anchor error intervals + drift slope bounds +
shared knot variables, i.e. explicit continuity) as a flat list of linear
inequalities over the knot reference values, then enumerates all candidate
vertices (n active constraints at a time) with exact Fraction Gaussian
elimination.  Exponential, but completely independent of the
difference-constraint / shortest-path machinery, so it serves as a
cross-check for feasibility and query bounds.
"""

from __future__ import annotations

from fractions import Fraction
from itertools import combinations


def build_inequalities(anchors, max_drift):
    """Return (num_vars, inequalities, knot_times).

    Each inequality is (coeffs, rhs) meaning sum_k coeffs[k]*x[k] <= rhs,
    where x[k] is the reference value at knot k (knots = sorted unique
    local times of `anchors`).
    """
    max_drift = Fraction(max_drift)
    times = sorted({a.local.seconds for a in anchors})
    index = {t: k for k, t in enumerate(times)}
    n = len(times)
    ineqs = []
    for a in anchors:
        k = index[a.local.seconds]
        up = [Fraction(0)] * n
        up[k] = Fraction(1)
        ineqs.append((tuple(up), a.ref_hi))
        dn = [Fraction(0)] * n
        dn[k] = Fraction(-1)
        ineqs.append((tuple(dn), -a.ref_lo))
    for k in range(n - 1):
        dt = times[k + 1] - times[k]
        up = [Fraction(0)] * n
        up[k], up[k + 1] = Fraction(-1), Fraction(1)
        ineqs.append((tuple(up), (1 + max_drift) * dt))
        dn = [Fraction(0)] * n
        dn[k], dn[k + 1] = Fraction(1), Fraction(-1)
        ineqs.append((tuple(dn), -(1 - max_drift) * dt))
    return n, ineqs, times


def _solve_square(rows, rhs):
    """Exact Gaussian elimination; None if the system is singular."""
    n = len(rows)
    aug = [list(map(Fraction, rows[i])) + [Fraction(rhs[i])] for i in range(n)]
    for col in range(n):
        pivot = next((r for r in range(col, n) if aug[r][col] != 0), None)
        if pivot is None:
            return None
        aug[col], aug[pivot] = aug[pivot], aug[col]
        inv = 1 / aug[col][col]
        aug[col] = [v * inv for v in aug[col]]
        for r in range(n):
            if r != col and aug[r][col] != 0:
                factor = aug[r][col]
                aug[r] = [v - factor * w for v, w in zip(aug[r], aug[col])]
    return [aug[i][n] for i in range(n)]


def vertices(num_vars, ineqs):
    """Yield all feasible vertices of the (bounded) constraint polytope."""
    if num_vars == 0:
        return
    for active in combinations(range(len(ineqs)), num_vars):
        rows = [ineqs[i][0] for i in active]
        rhs = [ineqs[i][1] for i in active]
        point = _solve_square(rows, rhs)
        if point is None:
            continue
        if all(sum(c * x for c, x in zip(coeffs, point)) <= r
               for coeffs, r in ineqs):
            yield tuple(point)


def feasible(num_vars, ineqs):
    if num_vars == 0:
        return True
    return any(True for _ in vertices(num_vars, ineqs))


def objective_range(num_vars, ineqs, objective):
    """Exact (min, max) of a linear objective, or None if infeasible."""
    lo = hi = None
    for point in vertices(num_vars, ineqs):
        value = sum(c * x for c, x in zip(objective, point))
        lo = value if lo is None or value < lo else lo
        hi = value if hi is None or value > hi else hi
    return None if lo is None else (lo, hi)
