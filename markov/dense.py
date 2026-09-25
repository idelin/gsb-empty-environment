"""Small dense linear algebra, standard library only.

Provides an LU factorization reusable after row revisions (Woodbury
rank-k update) and an exact rational solver over fractions.Fraction
used as the reference for small models.
"""

from fractions import Fraction


def lu_factor(a):
    """LU with partial pivoting; returns (lu, perm) with PA = LU."""
    n = len(a)
    lu = [list(map(float, row)) for row in a]
    perm = list(range(n))
    for k in range(n):
        pivot = max(range(k, n), key=lambda r: abs(lu[r][k]))
        if lu[pivot][k] == 0.0:
            raise ValueError("singular matrix")
        if pivot != k:
            lu[k], lu[pivot] = lu[pivot], lu[k]
            perm[k], perm[pivot] = perm[pivot], perm[k]
        for r in range(k + 1, n):
            factor = lu[r][k] / lu[k][k]
            lu[r][k] = factor
            for c in range(k + 1, n):
                lu[r][c] -= factor * lu[k][c]
    return lu, perm


def lu_solve(lu, perm, b):
    n = len(lu)
    x = [float(b[perm[i]]) for i in range(n)]
    for i in range(n):
        for j in range(i):
            x[i] -= lu[i][j] * x[j]
    for i in range(n - 1, -1, -1):
        for j in range(i + 1, n):
            x[i] -= lu[i][j] * x[j]
        x[i] /= lu[i][i]
    return x


def _dense_solve(a, b):
    """Small Gaussian elimination with partial pivoting (floats)."""
    n = len(a)
    m = [list(map(float, a[i])) + [float(b[i])] for i in range(n)]
    for k in range(n):
        pivot = max(range(k, n), key=lambda r: abs(m[r][k]))
        if m[pivot][k] == 0.0:
            raise ValueError("singular matrix")
        m[k], m[pivot] = m[pivot], m[k]
        for r in range(k + 1, n):
            factor = m[r][k] / m[k][k]
            for c in range(k, n + 1):
                m[r][c] -= factor * m[k][c]
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        x[i] = (m[i][n] - sum(m[i][j] * x[j] for j in range(i + 1, n)))
        x[i] /= m[i][i]
    return x


class LUSolver:
    """Reusable LU factorization of a dense square matrix."""

    def __init__(self, a):
        self.n = len(a)
        self.matrix = [list(map(float, row)) for row in a]
        self._lu, self._perm = lu_factor(a)

    def solve(self, b):
        if len(b) != self.n:
            raise ValueError("right-hand side has wrong length")
        return lu_solve(self._lu, self._perm, b)

    def solve_with_row_revisions(self, revisions, b):
        """Solve the row-revised system reusing this factorization.

        revisions: {row_index: new_row}.  The update A' = A + U V with
        U = [e_i ...] and V = (new_row - old_row) is applied through the
        Woodbury identity, so the base LU is factored only once.
        """
        if not revisions:
            return self.solve(b)
        rows = sorted(revisions)
        deltas = []
        for i in rows:
            if len(revisions[i]) != self.n:
                raise ValueError("revised row has wrong length")
            deltas.append([float(revisions[i][c]) - self.matrix[i][c]
                           for c in range(self.n)])
        y0 = self.solve(b)
        wcols = []
        for i in rows:
            e = [0.0] * self.n
            e[i] = 1.0
            wcols.append(self.solve(e))
        k = len(rows)
        m = [[sum(deltas[p][c] * wcols[q][c] for c in range(self.n))
              for q in range(k)] for p in range(k)]
        s = [sum(deltas[p][c] * y0[c] for c in range(self.n))
             for p in range(k)]
        for p in range(k):
            m[p][p] += 1.0
        z = _dense_solve(m, s)
        x = list(y0)
        for q in range(k):
            for c in range(self.n):
                x[c] -= z[q] * wcols[q][c]
        return x


def exact_solve(a, b):
    """Exact Gauss-Jordan solve over fractions.Fraction (small models)."""
    n = len(a)
    m = [[Fraction(v) for v in a[i]] + [Fraction(b[i])] for i in range(n)]
    for k in range(n):
        pivot = None
        for r in range(k, n):
            if m[r][k] != 0:
                pivot = r
                break
        if pivot is None:
            raise ValueError("singular matrix")
        m[k], m[pivot] = m[pivot], m[k]
        for r in range(n):
            if r != k and m[r][k] != 0:
                factor = m[r][k] / m[k][k]
                for c in range(k, n + 1):
                    m[r][c] -= factor * m[k][c]
    return [m[i][n] / m[i][i] for i in range(n)]
