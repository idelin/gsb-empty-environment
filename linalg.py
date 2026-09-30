"""Minimal pure-Python dense linear algebra (standard library only).

Only small dense matrices are supported; this module exists to serve the
low-dimensional channel matrices that arise in randomized response.
Matrices are represented as lists of row lists of floats.
"""

import math

_TOL = 1e-12


def transpose(a):
    return [list(row) for row in zip(*a)]


def matmul(a, b):
    bt = transpose(b)
    return [[sum(x * y for x, y in zip(row, col)) for col in bt] for row in a]


def matvec(a, x):
    return [sum(aij * xj for aij, xj in zip(row, x)) for row in a]


def identity(n):
    return [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]


def rref(a, tol=_TOL):
    """Reduced row echelon form. Returns (rref_matrix, pivot_columns)."""
    m = [list(row) for row in a]
    rows = len(m)
    cols = len(m[0]) if rows else 0
    pivots = []
    r = 0
    for c in range(cols):
        if r >= rows:
            break
        pivot = max(range(r, rows), key=lambda i: abs(m[i][c]))
        if abs(m[pivot][c]) <= tol:
            continue
        m[r], m[pivot] = m[pivot], m[r]
        pv = m[r][c]
        m[r] = [v / pv for v in m[r]]
        for i in range(rows):
            if i != r and abs(m[i][c]) > tol:
                factor = m[i][c]
                m[i] = [v - factor * w for v, w in zip(m[i], m[r])]
        pivots.append(c)
        r += 1
    return m, pivots


def rank(a, tol=_TOL):
    return len(rref(a, tol)[1])


def null_space(a, tol=_TOL):
    """Basis of the null space of ``a`` as a list of column vectors."""
    m, pivots = rref(a, tol)
    cols = len(a[0]) if a else 0
    free = [c for c in range(cols) if c not in pivots]
    basis = []
    for f in free:
        vec = [0.0] * cols
        vec[f] = 1.0
        for row_idx, p in enumerate(pivots):
            vec[p] = -m[row_idx][f]
        basis.append(vec)
    return basis


def inverse(a, tol=_TOL):
    """Gauss-Jordan inverse. Raises ValueError if (numerically) singular."""
    n = len(a)
    aug = [list(row) + list(eye) for row, eye in zip(a, identity(n))]
    for c in range(n):
        pivot = max(range(c, n), key=lambda i: abs(aug[i][c]))
        if abs(aug[pivot][c]) <= tol:
            raise ValueError("matrix is singular")
        aug[c], aug[pivot] = aug[pivot], aug[c]
        pv = aug[c][c]
        aug[c] = [v / pv for v in aug[c]]
        for i in range(n):
            if i != c:
                factor = aug[i][c]
                if factor != 0.0:
                    aug[i] = [v - factor * w for v, w in zip(aug[i], aug[c])]
    return [row[n:] for row in aug]


def jacobi_eigenvalues(m, tol=1e-14, max_sweeps=200):
    """Eigenvalues (descending) of a real symmetric matrix, cyclic Jacobi."""
    n = len(m)
    a = [list(row) for row in m]
    for _ in range(max_sweeps):
        off = math.sqrt(sum(a[i][j] ** 2 for i in range(n)
                            for j in range(n) if i != j))
        if off <= tol:
            break
        for p in range(n - 1):
            for q in range(p + 1, n):
                if abs(a[p][q]) <= tol:
                    continue
                theta = (a[q][q] - a[p][p]) / (2.0 * a[p][q])
                sign = 1.0 if theta >= 0 else -1.0
                t = sign / (abs(theta) + math.sqrt(theta * theta + 1.0))
                c = 1.0 / math.sqrt(t * t + 1.0)
                s = t * c
                for k in range(n):
                    akp, akq = a[k][p], a[k][q]
                    a[k][p] = c * akp - s * akq
                    a[k][q] = s * akp + c * akq
                for k in range(n):
                    apk, aqk = a[p][k], a[q][k]
                    a[p][k] = c * apk - s * aqk
                    a[q][k] = s * apk + c * aqk
    return sorted((a[i][i] for i in range(n)), reverse=True)


def singular_values(a):
    ata = matmul(transpose(a), a)
    return [math.sqrt(max(e, 0.0)) for e in jacobi_eigenvalues(ata)]


def condition_number(a, tol=1e-12):
    """2-norm condition number; inf when the matrix is rank deficient."""
    sv = singular_values(a)
    if not sv:
        return float("inf")
    smax, smin = sv[0], sv[-1]
    if smin <= tol * max(smax, 1.0):
        return float("inf")
    return smax / smin
