"""Dense linear algebra with explicit sizes and indices.

No NumPy/SciPy. Every operator takes the dimension ``n`` explicitly and
validates operand shapes before computing.
"""

import math


def _check_vec(n, v, name):
    if len(v) != n:
        raise ValueError(f"{name}: expected length {n}, got {len(v)}")


def _check_mat(n, a, name):
    if len(a) != n:
        raise ValueError(f"{name}: expected {n} rows, got {len(a)}")
    for i, row in enumerate(a):
        if len(row) != n:
            raise ValueError(f"{name}: row {i} expected length {n}, got {len(row)}")


def matvec(n, a, x):
    """y = A x for an n x n row-major matrix A."""
    _check_mat(n, a, "a")
    _check_vec(n, x, "x")
    return [sum(a[i][j] * x[j] for j in range(n)) for i in range(n)]


def dot(n, x, y):
    _check_vec(n, x, "x")
    _check_vec(n, y, "y")
    return sum(x[i] * y[i] for i in range(n))


def norm2(n, x):
    _check_vec(n, x, "x")
    return math.sqrt(sum(x[i] * x[i] for i in range(n)))


def sub(n, x, y):
    """x - y elementwise."""
    _check_vec(n, x, "x")
    _check_vec(n, y, "y")
    return [x[i] - y[i] for i in range(n)]


def solve_dense(n, a, b):
    """Direct solve A x = b by Gaussian elimination with partial pivoting.

    Used as the independent reference to verify the iterative solvers.
    """
    _check_mat(n, a, "a")
    _check_vec(n, b, "b")
    m = [list(a[i]) + [b[i]] for i in range(n)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(m[r][col]))
        if abs(m[piv][col]) == 0.0:
            raise ValueError(f"singular matrix at column {col}")
        m[col], m[piv] = m[piv], m[col]
        inv = 1.0 / m[col][col]
        for j in range(col, n + 1):
            m[col][j] *= inv
        for r in range(n):
            if r != col and m[r][col] != 0.0:
                factor = m[r][col]
                for j in range(col, n + 1):
                    m[r][j] -= factor * m[col][j]
    return [m[i][n] for i in range(n)]
