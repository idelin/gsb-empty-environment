"""Independent high-precision reference for small problems.

Uses exact rational arithmetic (fractions.Fraction) on the regularized
normal equations. Inputs are converted through ``str`` so decimal inputs
like 1e-6 are interpreted as their exact decimal value. Stdlib only.
"""

from fractions import Fraction

__all__ = ["exact_normal_matrices", "exact_solve"]


def _F(x):
    return Fraction(str(x))


def exact_normal_matrices(rows, weights, ys, regularization=0.0):
    """Return (M, v) with M = A^T W A + lam*I, v = A^T W y as Fractions."""
    n = len(rows[0])
    lam = _F(regularization)
    M = [[Fraction(0)] * n for _ in range(n)]
    v = [Fraction(0)] * n
    for i in range(n):
        M[i][i] = lam
    for row, w, y in zip(rows, weights, ys):
        w = _F(w)
        row = [_F(a) for a in row]
        y = _F(y)
        for i in range(n):
            wi = w * row[i]
            for j in range(n):
                M[i][j] += wi * row[j]
            v[i] += wi * y
    return M, v


def exact_solve(rows, weights, ys, regularization=0.0):
    """Solve the regularized normal equations exactly (must be nonsingular).

    Returns a list of Fractions. Raises ArithmeticError if singular.
    """
    M, v = exact_normal_matrices(rows, weights, ys, regularization)
    n = len(M)
    aug = [M[i][:] + [v[i]] for i in range(n)]
    for col in range(n):
        piv = next((r for r in range(col, n) if aug[r][col] != 0), None)
        if piv is None:
            raise ArithmeticError("reference system is singular")
        aug[col], aug[piv] = aug[piv], aug[col]
        inv = Fraction(1) / aug[col][col]
        aug[col] = [x * inv for x in aug[col]]
        for r in range(n):
            if r != col and aug[r][col] != 0:
                f = aug[r][col]
                aug[r] = [rv - f * pv for rv, pv in zip(aug[r], aug[col])]
    return [aug[i][n] for i in range(n)]


def max_abs_error(approx, exact):
    """Max |approx - exact| against a Fraction reference solution."""
    return max(abs(a - float(e)) for a, e in zip(approx, exact))
