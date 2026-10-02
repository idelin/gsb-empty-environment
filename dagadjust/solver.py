"""Weighted least-squares adjustment of noisy counts on a counting DAG.

    minimize    f(x) = 1/2 * sum_i w_i (x_i - y_i)^2
    subject to  C x = d        (DAG balance rows + fixed-count rows)
                x >= 0         (optional)

All arithmetic is exact rational (fractions.Fraction).  Termination is
decided solely by exact KKT certificates; a small change between
iterates is never treated as optimality.  If the iteration cap is hit,
the status is "not_converged" and no optimality claim is made.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .certificates import build_certificates
from .linalg import (
    SingularMatrixError,
    find_feasible_point,
    independent_rows,
    is_consistent,
    solve_square,
)

STATUS_OPTIMAL = "optimal"
STATUS_NOT_CONVERGED = "not_converged"
STATUS_INFEASIBLE = "infeasible"
STATUS_SINGULAR = "singular"


@dataclass
class SolveResult:
    status: str                       # one of the STATUS_* constants
    x: object                         # adjusted counts (None unless optimal)
    residuals: object                 # x - y (None unless optimal)
    objective: object                 # 1/2 * sum w_i (x_i - y_i)^2
    active_set: list                  # indices pinned to x_i = 0
    iterations: int                   # EQP solves performed
    message: str
    certificates: object              # Certificates or None
    zero_weight_variables: list       # indices whose observation is ignored
    redundant_constraints: list       # row indices dropped as redundant

    @property
    def is_optimal(self):
        return (
            self.status == STATUS_OPTIMAL
            and self.certificates is not None
            and self.certificates.verified
        )


def solve_eqp(w, y, E, f):
    """Solve  min 1/2 (x-y)'W(x-y)  s.t. E x = f  via its KKT system.

    Returns (x, lambda).  Raises SingularMatrixError when the KKT
    matrix is singular (e.g. zero-weight variables left undetermined).
    """
    n = len(y)
    p = len(E)
    size = n + p
    K = [[Fraction(0)] * size for _ in range(size)]
    for i in range(n):
        K[i][i] = w[i]
    for r in range(p):
        row = E[r]
        for c in range(n):
            if row[c]:
                K[c][n + r] = row[c]
                K[n + r][c] = row[c]
    rhs = [w[i] * y[i] for i in range(n)] + list(f)
    sol = solve_square(K, rhs)
    return sol[:n], sol[n:]


def _active_set(w, y, C, d, max_iterations, warm_active):
    """Primal active-set loop for bound constraints x >= 0.

    Each iteration solves one equality-constrained QP exactly.  The
    loop stops only when the current point satisfies every KKT
    condition; it never stops because an iterate barely moved.
    """
    n = len(y)
    active = []
    for j in warm_active:
        if 0 <= j < n and j not in active:
            active.append(j)
    iterations = 0
    while iterations < max_iterations:
        iterations += 1
        E = [list(row) for row in C]
        f = list(d)
        for j in active:
            row = [Fraction(0)] * n
            row[j] = Fraction(1)
            E.append(row)
            f.append(Fraction(0))
        x, lam_all = solve_eqp(w, y, E, f)
        lam = lam_all[: len(C)]
        nu = lam_all[len(C):]  # multipliers of the x_j = 0 rows
        violated = [j for j in range(n) if j not in active and x[j] < 0]
        if violated:
            worst = min(violated, key=lambda j: (x[j], j))
            active.append(worst)
            continue
        drop = None
        worst_mu = Fraction(0)
        for j, nu_j in zip(active, nu):
            mu_j = -nu_j  # bound multiplier for x_j >= 0
            if mu_j < worst_mu:
                worst_mu = mu_j
                drop = j
        if drop is not None:
            active.remove(drop)
            continue
        mu = [Fraction(0)] * n
        for j, nu_j in zip(active, nu):
            mu[j] = -nu_j
        return x, lam, mu, sorted(active), iterations, True
    return None, None, None, sorted(active), iterations, False


def _describe_free_columns(free, n):
    parts = []
    for c in free:
        if c < n:
            parts.append(f"variable x{c}")
        else:
            parts.append(f"multiplier column {c - n}")
    return ", ".join(parts)


def _result(status, message, zero_weight, redundant, **kw):
    base = dict(
        x=None,
        residuals=None,
        objective=None,
        active_set=[],
        iterations=0,
        certificates=None,
    )
    base.update(kw)
    return SolveResult(
        status=status,
        message=message,
        zero_weight_variables=zero_weight,
        redundant_constraints=redundant,
        **base,
    )


def solve(problem, max_iterations=None, warm_start=None):
    """Solve the adjustment problem exactly.

    Parameters
    ----------
    problem : AdjustmentProblem
    max_iterations : int or None
        Cap on EQP solves.  If the cap is reached the status is
        "not_converged" and the result must not be called optimal.
    warm_start : SolveResult or iterable of int or None
        Active set from a previous solve (e.g. after a local
        observation revision).  Only used as a starting guess for the
        working set; certificates are always recomputed.
    """
    n = problem.dag.n_nodes
    y = list(problem.observed)
    w = list(problem.weights)
    zero_weight = [i for i, wi in enumerate(w) if wi == 0]

    rows, rhs, _labels = problem.constraints()

    if problem.nonneg:
        bad = [i for i, v in problem.fixed if v < 0]
        if bad:
            return _result(
                STATUS_INFEASIBLE,
                f"fixed count is negative while x >= 0 is required: variables {bad}",
                zero_weight,
                [],
            )
    if not is_consistent(rows, rhs):
        return _result(
            STATUS_INFEASIBLE,
            "equality constraints are inconsistent (check fixed counts)",
            zero_weight,
            [],
        )
    reduced_rows, kept, redundant = independent_rows(rows)
    reduced_rhs = [rhs[i] for i in kept]

    if problem.nonneg and find_feasible_point(rows, rhs) is None:
        return _result(
            STATUS_INFEASIBLE,
            "no nonnegative point satisfies the constraints (check fixed counts)",
            zero_weight,
            redundant,
        )

    if max_iterations is None:
        max_iterations = max(100, 5 * (n + len(reduced_rows)))
    if max_iterations <= 0:
        return _result(
            STATUS_NOT_CONVERGED,
            f"iteration cap {max_iterations} allows no EQP solve; "
            "result is NOT certified optimal",
            zero_weight,
            redundant,
        )

    warm_active = []
    if warm_start is not None:
        if isinstance(warm_start, SolveResult):
            warm_active = list(warm_start.active_set)
        else:
            warm_active = list(warm_start)

    try:
        if problem.nonneg:
            x, lam, mu, active, iterations, converged = _active_set(
                w, y, reduced_rows, reduced_rhs, max_iterations, warm_active
            )
        else:
            x, lam = solve_eqp(w, y, reduced_rows, reduced_rhs)
            mu = [Fraction(0)] * n
            active, iterations, converged = [], 1, True
    except SingularMatrixError as exc:
        detail = _describe_free_columns(exc.free_columns, n)
        return _result(
            STATUS_SINGULAR,
            "KKT system is singular (zero-weight or otherwise "
            f"underdetermined): {detail}",
            zero_weight,
            redundant,
        )

    if not converged:
        return _result(
            STATUS_NOT_CONVERGED,
            f"iteration cap {max_iterations} reached after {iterations} "
            "EQP solves; result is NOT certified optimal",
            zero_weight,
            redundant,
            iterations=iterations,
        )

    certificates = build_certificates(problem, rows, rhs, reduced_rows, x, lam, mu)
    residuals = [x[i] - y[i] for i in range(n)]
    objective = sum(w[i] * residuals[i] ** 2 for i in range(n)) / 2
    status = STATUS_OPTIMAL if certificates.verified else STATUS_NOT_CONVERGED
    message = (
        "KKT certificate verified exactly"
        if certificates.verified
        else "KKT certificate failed; result is NOT certified optimal"
    )
    return _result(
        status,
        message,
        zero_weight,
        redundant,
        x=x,
        residuals=residuals,
        objective=objective,
        active_set=active,
        iterations=iterations,
        certificates=certificates,
    )
