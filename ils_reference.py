"""Independent high-precision reference implementation.

Builds the normal equations with exact rational arithmetic
(fractions.Fraction) and solves them with exact Gaussian elimination.
Used to cross-check the float64 incremental solver on small problems;
float inputs are converted exactly, so the comparison isolates the
algorithmic rounding error of the solver under test.
"""

from fractions import Fraction


def exact_normal_equations(observations, n, regularization=0.0):
    """Return (M, z) as exact Fractions.

    observations: iterable of (row, weight, target).
    """
    M = [[Fraction(0) for _ in range(n)] for _ in range(n)]
    z = [Fraction(0) for _ in range(n)]
    if regularization:
        lam = Fraction(regularization)
        for i in range(n):
            M[i][i] = lam
    for row, weight, target in observations:
        fr = [Fraction(v) for v in row]
        fw = Fraction(weight)
        ft = Fraction(target)
        for i in range(n):
            wi = fw * fr[i]
            z[i] += wi * ft
            for j in range(n):
                M[i][j] += wi * fr[j]
    return M, z


def exact_solve(M, z):
    """Solve M x = z exactly (Gauss-Jordan over Fractions)."""
    n = len(M)
    aug = [list(M[i]) + [z[i]] for i in range(n)]
    for col in range(n):
        pivot = max(range(col, n), key=lambda r: abs(aug[r][col]))
        if aug[pivot][col] == 0:
            raise ArithmeticError("singular matrix in exact reference")
        aug[col], aug[pivot] = aug[pivot], aug[col]
        for r in range(n):
            if r != col and aug[r][col] != 0:
                factor = aug[r][col] / aug[col][col]
                aug[r] = [a - factor * b for a, b in zip(aug[r], aug[col])]
    return [aug[i][n] / aug[i][i] for i in range(n)]


def max_relative_error(x, x_exact):
    """max_i |x_i - exact_i| / max(1, |exact_i|), computed exactly."""
    worst = Fraction(0)
    for a, b in zip(x, x_exact):
        diff = abs(Fraction(a) - b)
        denom = max(abs(b), Fraction(1))
        if diff / denom > worst:
            worst = diff / denom
    return float(worst)
