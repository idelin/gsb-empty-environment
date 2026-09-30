"""Exact rational quadratic programming for consistent histogram release.

All arithmetic uses fractions.Fraction so results are exact and every
certificate can be re-checked independently with zero numerical tolerance.

Problem solved here:

    minimize    f(x) = sum_j  w_j * (a_j . x - v_j)^2
    subject to  sum_i x_i = total
                x_i >= 0

where each publication j is a noisy linear measurement of the leaf bucket
counts (a leaf count, or an overlapping group sum), w_j is a positive
rational weight, and `total` comes from an explicitly public source or a
separately noised measurement (decided by the caller, recorded upstream).
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import combinations
from typing import Optional


class InfeasibleError(Exception):
    """The constraint set {x >= 0, sum(x) = total} is empty."""


class SingularError(Exception):
    """A KKT system was singular (weights must make the objective strictly
    convex on the free variables; publishing every leaf with positive
    weight guarantees this)."""


@dataclass(frozen=True)
class Publication:
    """One noisy linear measurement: coeffs . x ~= value with given weight."""
    name: str
    coeffs: dict  # {leaf_index: Fraction}
    value: Fraction
    weight: Fraction


@dataclass
class QPSolution:
    x: list
    lam: Optional[Fraction]      # multiplier of the equality constraint
    mu: Optional[list]           # multipliers of the x_i >= 0 constraints
    status: str                  # "optimal" | "not_converged"
    iterations: int
    objective_value: Optional[Fraction]


def _fr(value) -> Fraction:
    return value if isinstance(value, Fraction) else Fraction(value)


def build_normal_equations(publications, n):
    """H = sum w a a^T, c = sum w v a, so grad f(x) = 2 (H x - c)."""
    H = [[Fraction(0)] * n for _ in range(n)]
    c = [Fraction(0)] * n
    for pub in publications:
        for i, ai in pub.coeffs.items():
            c[i] += pub.weight * ai * pub.value
            for j, aj in pub.coeffs.items():
                H[i][j] += pub.weight * ai * aj
    return H, c


def objective(publications, x) -> Fraction:
    total = Fraction(0)
    for pub in publications:
        residual = sum(a * x[i] for i, a in pub.coeffs.items()) - pub.value
        total += pub.weight * residual * residual
    return total


def gradient(publications, x):
    """grad f(x) = 2 (H x - c), computed directly from the publications."""
    g = [Fraction(0)] * len(x)
    for pub in publications:
        residual = sum(a * x[i] for i, a in pub.coeffs.items()) - pub.value
        for i, a in pub.coeffs.items():
            g[i] += 2 * pub.weight * a * residual
    return g


def _solve_linear(matrix, rhs):
    """Exact Gauss-Jordan elimination over Fraction."""
    n = len(rhs)
    aug = [list(matrix[i]) + [rhs[i]] for i in range(n)]
    for col in range(n):
        pivot = next((r for r in range(col, n) if aug[r][col] != 0), None)
        if pivot is None:
            raise SingularError("singular KKT system")
        aug[col], aug[pivot] = aug[pivot], aug[col]
        inv = 1 / aug[col][col]
        aug[col] = [v * inv for v in aug[col]]
        for r in range(n):
            if r != col and aug[r][col] != 0:
                factor = aug[r][col]
                aug[r] = [rv - factor * cv for rv, cv in zip(aug[r], aug[col])]
    return [aug[i][n] for i in range(n)]


def _solve_free(H, c, free, total):
    """Minimize f over the free indices with sum(x_free) = total, others 0.

    Solves the KKT system  [2 H_FF  1] [x]   [2 c_F]
                           [  1^T   0] [lam]=[total]
    """
    k = len(free)
    if k == 0:
        raise SingularError("empty free set")
    mat = [[Fraction(0)] * (k + 1) for _ in range(k + 1)]
    for a, i in enumerate(free):
        for b, j in enumerate(free):
            mat[a][b] = 2 * H[i][j]
        mat[a][k] = Fraction(1)
        mat[k][a] = Fraction(1)
    rhs = [2 * c[i] for i in free] + [total]
    sol = _solve_linear(mat, rhs)
    return sol[:k], sol[k]


def project_simplex(y, total):
    """Euclidean projection of y onto {x >= 0, sum(x) = total} (exact)."""
    values = [_fr(v) for v in y]
    t_total = _fr(total)
    n = len(values)
    if n == 0:
        raise InfeasibleError("no variables")
    if t_total < 0:
        raise InfeasibleError(f"total {t_total} < 0 is infeasible with x >= 0")
    ordered = sorted(values, reverse=True)
    cum = Fraction(0)
    theta = Fraction(0)
    for idx, v in enumerate(ordered, start=1):
        cum += v
        t = (cum - t_total) / idx
        if v - t > 0:
            theta = t
    zero = Fraction(0)
    return [max(v - theta, zero) for v in values]


def solve_iterative(publications, n, total, max_iterations=1000):
    """Active-set QP solver (exact rational arithmetic).

    The equality constraint is always kept; the working set holds the
    indices currently pinned to zero. Each iteration is exact, so the only
    reason not to converge within `max_iterations` is a too-small budget
    (or degenerate cycling), reported as status "not_converged".
    """
    t_total = _fr(total)
    if n <= 0:
        raise InfeasibleError("no variables")
    if t_total < 0:
        raise InfeasibleError(
            f"total {t_total} < 0 is infeasible with x >= 0")
    H, c = build_normal_equations(publications, n)

    if t_total == 0:
        # The only feasible point is x = 0; choose lam to certify it.
        x = [Fraction(0)] * n
        g = gradient(publications, x)
        lam = max(-gi for gi in g)
        mu = [gi + lam for gi in g]
        return QPSolution(x, lam, mu, "optimal", 0,
                          objective(publications, x))

    x = [t_total / n] * n
    zero = set()
    iterations = 0
    while True:
        if iterations >= max_iterations:
            return QPSolution(x, None, None, "not_converged",
                              iterations, None)
        iterations += 1
        free = [i for i in range(n) if i not in zero]
        x_free, lam = _solve_free(H, c, free, t_total)

        if all(v >= 0 for v in x_free):
            for k, i in enumerate(free):
                x[i] = x_free[k]
            g = gradient(publications, x)
            mu = [Fraction(0)] * n
            worst = Fraction(0)
            worst_i = None
            for i in zero:
                mu[i] = g[i] + lam
                if mu[i] < worst:
                    worst, worst_i = mu[i], i
            if worst_i is None:
                return QPSolution(list(x), lam, mu, "optimal", iterations,
                                  objective(publications, x))
            zero.discard(worst_i)
        else:
            direction = {i: x_free[k] - x[i] for k, i in enumerate(free)}
            alpha = Fraction(1)
            blockers = []
            for i, di in direction.items():
                if di < 0:
                    step = -x[i] / di
                    if step < alpha:
                        alpha, blockers = step, [i]
                    elif step == alpha:
                        blockers.append(i)
            for i, di in direction.items():
                x[i] += alpha * di
            zero.update(blockers)


def solve_by_enumeration(publications, n, total):
    """Reference solver: enumerate every active set (small n only).

    For each subset Z of indices pinned to zero, solves the equality-only
    subproblem on the complement and keeps the point if it is feasible and
    satisfies the full KKT conditions. Returns the best feasible KKT point.
    """
    t_total = _fr(total)
    if n <= 0:
        raise InfeasibleError("no variables")
    if t_total < 0:
        raise InfeasibleError(
            f"total {t_total} < 0 is infeasible with x >= 0")
    H, c = build_normal_equations(publications, n)
    best = None
    solved = 0
    indices = range(n)
    for r in range(0, n):
        for zero_set in combinations(indices, r):
            free = [i for i in indices if i not in zero_set]
            try:
                x_free, lam = _solve_free(H, c, free, t_total)
            except SingularError:
                continue
            solved += 1
            if any(v < 0 for v in x_free):
                continue
            x = [Fraction(0)] * n
            for k, i in enumerate(free):
                x[i] = x_free[k]
            g = gradient(publications, x)
            mu = [Fraction(0)] * n
            if any((g[i] + lam) < 0 for i in zero_set):
                continue
            for i in zero_set:
                mu[i] = g[i] + lam
            obj = objective(publications, x)
            if best is None or obj < best.objective_value:
                best = QPSolution(x, lam, mu, "optimal", solved, obj)
    if best is None:
        raise InfeasibleError("no feasible KKT point found")
    return best


def check_kkt(publications, n, total, x, lam, mu):
    """Independently verify a KKT certificate. Returns a list of violations
    (empty list means the certificate proves optimality)."""
    t_total = _fr(total)
    violations = []
    if len(x) != n or len(mu) != n:
        return ["length mismatch"]
    for i in range(n):
        if x[i] < 0:
            violations.append(f"x[{i}] = {x[i]} < 0 (primal infeasible)")
        if mu[i] < 0:
            violations.append(f"mu[{i}] = {mu[i]} < 0 (dual infeasible)")
        if x[i] * mu[i] != 0:
            violations.append(
                f"x[{i}] * mu[{i}] != 0 (complementary slackness)")
    if sum(x) != t_total:
        violations.append(f"sum(x) = {sum(x)} != total {t_total}")
    g = gradient(publications, x)
    for i in range(n):
        stationarity = g[i] + lam - mu[i]
        if stationarity != 0:
            violations.append(f"stationarity violated at {i}: {stationarity}")
    return violations
