"""Rank-1 and low-rank updates of a factorized dense linear system.

Pure standard library (no NumPy/SciPy).  A base matrix A is factorized once
with dense LU (partial pivoting).  Updated systems

    (A + u v^T) x = b          rank-1   (Sherman-Morrison)
    (A + U V^T) x = b          rank-k   (Woodbury)

are solved reusing the base factorization only.  If an update makes the
system singular (or numerically unstable, i.e. the Sherman-Morrison
denominator / capacitance matrix is near singular), a SingularUpdateError
is raised and the original factorization remains fully usable.

Sizes and indices are explicit inputs: vectors are given as (indices,
values) pairs together with the dimension n, so nothing is inferred from
container shapes.
"""

__all__ = [
    "SingularMatrixError",
    "SingularUpdateError",
    "LU",
    "lu_factorize",
    "dense_from_sparse",
    "matvec",
    "RankOneUpdate",
    "LowRankUpdate",
]


class SingularMatrixError(Exception):
    """Raised when a dense LU factorization hits a (near-)zero pivot."""


class SingularUpdateError(Exception):
    """Raised when a rank-1/low-rank update makes the system singular.

    The base factorization is untouched and stays usable by the caller.
    """

    def __init__(self, message, denominator=None):
        super().__init__(message)
        self.denominator = denominator


# ---------------------------------------------------------------------------
# small dense helpers
# ---------------------------------------------------------------------------

def dense_from_sparse(n, indices, values):
    """Build a dense vector of length n from explicit indices/values."""
    if len(indices) != len(values):
        raise ValueError("indices and values must have the same length")
    vec = [0.0] * n
    for i, val in zip(indices, values):
        if not 0 <= i < n:
            raise IndexError(f"index {i} out of range for dimension {n}")
        vec[i] += float(val)
    return vec


def dot(a, b):
    return sum(x * y for x, y in zip(a, b))


def matvec(a, x):
    """Dense matrix (list of rows) times vector."""
    return [dot(row, x) for row in a]


def _check_square(a, n):
    if len(a) != n or any(len(row) != n for row in a):
        raise ValueError(f"matrix must be {n}x{n}")


# ---------------------------------------------------------------------------
# dense LU with partial pivoting
# ---------------------------------------------------------------------------

class LU:
    """Dense LU factorization (partial pivoting) of an n x n matrix."""

    def __init__(self, a, n, tol=1e-12):
        _check_square(a, n)
        self.n = n
        self.tol = tol
        lu = [list(map(float, row)) for row in a]
        perm = list(range(n))
        for k in range(n):
            p = max(range(k, n), key=lambda i: abs(lu[i][k]))
            if abs(lu[p][k]) <= tol:
                raise SingularMatrixError(
                    f"matrix is singular (pivot {lu[p][k]:.3e} at column {k})"
                )
            if p != k:
                lu[k], lu[p] = lu[p], lu[k]
                perm[k], perm[p] = perm[p], perm[k]
            for i in range(k + 1, n):
                lu[i][k] /= lu[k][k]
                lik = lu[i][k]
                row_i, row_k = lu[i], lu[k]
                for j in range(k + 1, n):
                    row_i[j] -= lik * row_k[j]
        self._lu = lu
        self._perm = perm

    def solve(self, b):
        """Solve A x = b for a single right-hand side."""
        n = self.n
        if len(b) != n:
            raise ValueError(f"right-hand side must have length {n}")
        lu, perm = self._lu, self._perm
        x = [float(b[perm[i]]) for i in range(n)]
        for i in range(1, n):  # forward substitution (unit L)
            xi = x[i]
            row = lu[i]
            for j in range(i):
                xi -= row[j] * x[j]
            x[i] = xi
        for i in range(n - 1, -1, -1):  # back substitution (U)
            xi = x[i]
            row = lu[i]
            for j in range(i + 1, n):
                xi -= row[j] * x[j]
            x[i] = xi / row[i]
        return x

    def solve_many(self, rhs_list):
        """Solve A X = B for multiple right-hand sides (list of vectors)."""
        return [self.solve(b) for b in rhs_list]


def lu_factorize(a, n, tol=1e-12):
    """Convenience wrapper returning an LU factorization."""
    return LU(a, n, tol=tol)


# ---------------------------------------------------------------------------
# rank-1 update:  (A + u v^T) x = b   via Sherman-Morrison
# ---------------------------------------------------------------------------

class RankOneUpdate:
    """Solver for (A + u v^T) x = b built on top of a base solver.

    ``base`` is any object with attributes ``n`` and ``solve(b)`` (an LU
    factorization or another update), so updates compose: applying the
    update with (-u, v) on top of a RankOneUpdate reverses it.

    u and v are given as explicit (indices, values) pairs of length ``nnz``
    together with the dimension ``n``.
    """

    def __init__(self, base, u_indices, u_values, v_indices, v_values, n,
                 tol=1e-10):
        if n != base.n:
            raise ValueError(f"dimension mismatch: n={n}, base.n={base.n}")
        self.base = base
        self.n = n
        u = dense_from_sparse(n, u_indices, u_values)
        v = dense_from_sparse(n, v_indices, v_values)
        self.u, self.v = u, v
        # w = A^{-1} u  (one solve against the base factorization)
        self._w = base.solve(u)
        self._vtw = dot(v, self._w)
        self.denominator = 1.0 + self._vtw
        # Relative check: |1 + v^T A^{-1} u| must be safely away from 0.
        if abs(self.denominator) <= tol * max(1.0, abs(self._vtw)):
            raise SingularUpdateError(
                "rank-1 update is singular or unstable: "
                f"denominator 1 + v^T A^-1 u = {self.denominator:.3e}",
                denominator=self.denominator,
            )

    def solve(self, b):
        """Solve (A + u v^T) x = b."""
        y = self.base.solve(b)
        coeff = dot(self.v, y) / self.denominator
        return [yi - coeff * wi for yi, wi in zip(y, self._w)]

    def solve_many(self, rhs_list):
        return [self.solve(b) for b in rhs_list]

    def reverse_update(self, tol=1e-10):
        """Return the update that undoes this one: (A + u v^T) - u v^T = A."""
        n = self.n
        neg_u_idx = [i for i, ui in enumerate(self.u) if ui != 0.0]
        neg_u_val = [-self.u[i] for i in neg_u_idx]
        v_idx = [i for i, vi in enumerate(self.v) if vi != 0.0]
        v_val = [self.v[i] for i in v_idx]
        return RankOneUpdate(self, neg_u_idx, neg_u_val, v_idx, v_val, n,
                             tol=tol)


# ---------------------------------------------------------------------------
# low-rank update:  (A + U V^T) x = b   via Woodbury
# ---------------------------------------------------------------------------

class LowRankUpdate:
    """Solver for (A + U V^T) x = b with U, V of size n x k (k << n).

    ``u_cols`` / ``v_cols`` are lists of k sparse columns, each given as an
    (indices, values) pair.  The k x k capacitance matrix
    C = I + V^T A^{-1} U is factorized with the same dense LU routine; if
    it is (near-)singular a SingularUpdateError is raised and the base
    factorization is left untouched.
    """

    def __init__(self, base, u_cols, v_cols, n, k, tol=1e-10):
        if n != base.n:
            raise ValueError(f"dimension mismatch: n={n}, base.n={base.n}")
        if len(u_cols) != k or len(v_cols) != k:
            raise ValueError(f"expected {k} columns in U and V")
        self.base = base
        self.n = n
        self.k = k
        self.U = [dense_from_sparse(n, idx, val) for idx, val in u_cols]
        self.V = [dense_from_sparse(n, idx, val) for idx, val in v_cols]
        # W = A^{-1} U  (k solves against the base factorization)
        self._W = [base.solve(uj) for uj in self.U]
        # Capacitance matrix C = I + V^T W  (k x k)
        c = [[(1.0 if i == j else 0.0) + dot(self.V[i], self._W[j])
              for j in range(k)] for i in range(k)]
        try:
            self._c_lu = LU(c, k, tol=tol)
        except SingularMatrixError as exc:
            raise SingularUpdateError(
                f"low-rank update is singular or unstable: {exc}"
            ) from exc

    def solve(self, b):
        """Solve (A + U V^T) x = b."""
        y = self.base.solve(b)
        z = self._c_lu.solve([dot(vi, y) for vi in self.V])
        x = list(y)
        for zj, wj in zip(z, self._W):
            if zj != 0.0:
                x = [xi - zj * wij for xi, wij in zip(x, wj)]
        return x

    def solve_many(self, rhs_list):
        return [self.solve(b) for b in rhs_list]
