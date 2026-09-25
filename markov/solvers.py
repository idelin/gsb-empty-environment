"""Linear solvers: dense LU with row-revision reuse, sparse SOR iteration."""

from __future__ import annotations

from dataclasses import dataclass

from .dense import lu_factorize, lu_solve, residual_vector


@dataclass
class IterativeResult:
    """Outcome of an iterative solve.

    ``converged`` is True only when the *true residual* met the tolerance;
    a small increment alone never counts as convergence.
    """

    x: list
    converged: bool
    iterations: int
    residual_norm: float    # max |b + Q x - x| at the returned iterate
    increment_norm: float   # max |x_k - x_{k-1}| of the last sweep
    message: str = ""


def assess_convergence(residual_norm, increment_norm, tol):
    """Decide convergence from the true residual, never from increments.

    Returns ``(converged, warning)``.  A small increment ``|x_{k+1} - x_k|``
    is necessary but not sufficient: slowly converging iterations can make
    tiny steps while still being far from the solution, so when the increment
    is below ``tol`` but the residual is not, a warning is produced.
    """
    if residual_norm <= tol:
        return True, ""
    if increment_norm <= tol:
        return False, (
            "increment is below tolerance but the true residual is not; "
            "a small change between iterates is not proof of convergence"
        )
    return False, ""


def solve_additive_sparse(q_rows, b, tol=1e-12, max_iter=100000, omega=1.0, x0=None):
    """Solve ``x = b + Q x`` (i.e. ``(I - Q) x = b``) by SOR on sparse rows.

    ``q_rows[i]`` is ``{column: value}``.  ``omega = 1`` gives Gauss-Seidel;
    ``1 < omega < 2`` over-relaxes (useful for large random-walk chains).

    The sweep stops only when the true residual ``max |b + Q x - x|`` is at
    most ``tol``.  The returned result always carries both the residual and
    the last increment so callers can apply :func:`assess_convergence`.
    """
    n = len(b)
    x = list(x0) if x0 is not None else [0.0] * n
    if not 0.0 < omega < 2.0:
        raise ValueError("omega must lie in (0, 2)")
    increment = float("inf")
    residual = float("inf")
    for iteration in range(1, max_iter + 1):
        increment = 0.0
        for i in range(n):
            s = b[i]
            for j, q in q_rows[i].items():
                s += q * x[j]
            s = x[i] + omega * (s - x[i])
            change = abs(s - x[i])
            if change > increment:
                increment = change
            x[i] = s
        residual = 0.0
        for i in range(n):
            s = b[i]
            for j, q in q_rows[i].items():
                s += q * x[j]
            r = abs(s - x[i])
            if r > residual:
                residual = r
        if residual <= tol:
            return IterativeResult(
                x, True, iteration, residual, increment,
                "converged: true residual within tolerance",
            )
    _, warning = assess_convergence(residual, increment, tol)
    message = f"maximum iterations ({max_iter}) reached"
    if warning:
        message += "; " + warning
    return IterativeResult(x, False, max_iter, residual, increment, message)


class LUSolver:
    """Dense LU solver supporting row revision with factorization reuse.

    After :meth:`update_row`, the next :meth:`solve` first attempts iterative
    refinement using the existing (stale) factorization as an approximate
    inverse of the revised matrix.  If the true residual fails to decrease,
    the matrix is refactorized.  ``last_solve_reused`` reports which path the
    most recent solve took, and ``refinement_iterations`` how many refinement
    steps were needed.
    """

    def __init__(self, a):
        self.n = len(a)
        self.a = [list(row) for row in a]
        self._lu, self._perm = lu_factorize(self.a)
        self._stale = False
        self.last_solve_reused = False
        self.refinement_iterations = 0

    def update_row(self, i, new_row):
        """Replace row ``i`` of the matrix; the factorization is kept stale."""
        if len(new_row) != self.n:
            raise ValueError("replacement row has wrong length")
        self.a[i] = list(new_row)
        self._stale = True

    def solve(self, b, tol=1e-13, max_refinement=60):
        if not self._stale:
            self.last_solve_reused = False
            self.refinement_iterations = 0
            return lu_solve(self._lu, self._perm, b)
        x = lu_solve(self._lu, self._perm, b)
        scale = max(1.0, max(abs(bi) for bi in b))
        previous = None
        for k in range(1, max_refinement + 1):
            r = residual_vector(self.a, x, b)
            residual = max(abs(ri) for ri in r)
            if residual <= tol * scale:
                self.last_solve_reused = True
                self.refinement_iterations = k - 1
                return x
            if previous is not None and residual > previous * 1.5:
                break  # refinement diverging: refactorize below
            previous = residual
            dx = lu_solve(self._lu, self._perm, r)
            x = [xi + dxi for xi, dxi in zip(x, dx)]
        self._lu, self._perm = lu_factorize(self.a)
        self._stale = False
        self.last_solve_reused = False
        self.refinement_iterations = 0
        return lu_solve(self._lu, self._perm, b)
