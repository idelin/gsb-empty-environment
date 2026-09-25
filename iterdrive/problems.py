"""Sample problems and reference solutions for verification."""

from .dense import matvec, solve_dense


def poisson_problem(n):
    """1D Poisson matrix (2 on diagonal, -1 off-diagonal), x_true = ones."""
    if n < 2:
        raise ValueError("n must be >= 2")
    a = [[0.0] * n for _ in range(n)]
    for i in range(n):
        a[i][i] = 2.0
        if i > 0:
            a[i][i - 1] = -1.0
        if i < n - 1:
            a[i][i + 1] = -1.0
    x_true = [1.0] * n
    b = matvec(n, a, x_true)
    problem = {"n": n, "a": a, "b": b}
    return problem, x_true


def reference_solve(problem):
    """Independent dense direct solve used to verify iterative results."""
    n = problem["n"]
    return solve_dense(n, problem["a"], problem["b"])
