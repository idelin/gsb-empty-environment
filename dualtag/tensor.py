"""Minimal dense tensors and small linear solves -- pure standard library.

Only what the implicit solver and the tests need: elementwise arithmetic,
matmul, norms, and Gaussian elimination with partial pivoting that reports
singularity instead of silently producing garbage.
"""
from __future__ import annotations

import math

from .errors import SingularJacobianError


class Tensor:
    def __init__(self, data, shape):
        self.data = [float(v) for v in data]
        self.shape = tuple(shape)
        n = 1
        for s in self.shape:
            n *= s
        if n != len(self.data):
            raise ValueError(f"shape {self.shape} does not match {len(self.data)} values")

    @staticmethod
    def zeros(shape):
        n = 1
        for s in shape:
            n *= s
        return Tensor([0.0] * n, shape)

    @staticmethod
    def from_rows(rows):
        rows = [list(r) for r in rows]
        if not rows:
            raise ValueError("need at least one row")
        ncols = len(rows[0])
        if any(len(r) != ncols for r in rows):
            raise ValueError("ragged rows")
        return Tensor([v for r in rows for v in r], (len(rows), ncols))

    @staticmethod
    def identity(n):
        return Tensor.from_rows([[1.0 if i == j else 0.0 for j in range(n)]
                                 for i in range(n)])

    @property
    def ndim(self):
        return len(self.shape)

    @property
    def size(self):
        return len(self.data)

    def to_rows(self):
        if self.ndim == 1:
            return [list(self.data)]
        if self.ndim != 2:
            raise ValueError("to_rows needs a 1- or 2-D tensor")
        r, c = self.shape
        return [self.data[i * c:(i + 1) * c] for i in range(r)]

    def _check_same_shape(self, other):
        if self.shape != other.shape:
            raise ValueError(f"shape mismatch {self.shape} vs {other.shape}")

    def __add__(self, other):
        self._check_same_shape(other)
        return Tensor([a + b for a, b in zip(self.data, other.data)], self.shape)

    def __sub__(self, other):
        self._check_same_shape(other)
        return Tensor([a - b for a, b in zip(self.data, other.data)], self.shape)

    def scale(self, k):
        return Tensor([k * v for v in self.data], self.shape)

    def map(self, fn):
        return Tensor([fn(v) for v in self.data], self.shape)

    def matmul(self, other):
        if self.ndim != 2 or other.ndim != 2:
            raise ValueError("matmul needs two 2-D tensors")
        m, k = self.shape
        k2, n = other.shape
        if k != k2:
            raise ValueError(f"inner dimensions differ: {self.shape} vs {other.shape}")
        a, b = self.to_rows(), other.to_rows()
        out = [[sum(a[i][p] * b[p][j] for p in range(k)) for j in range(n)]
               for i in range(m)]
        return Tensor.from_rows(out)

    def norm(self):
        return math.sqrt(sum(v * v for v in self.data))

    def almost_equal(self, other, tol=1e-9):
        self._check_same_shape(other)
        return all(abs(a - b) <= tol for a, b in zip(self.data, other.data))

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data})"


def _as_rows(A):
    if isinstance(A, Tensor):
        if A.ndim != 2:
            raise ValueError("need a 2-D matrix")
        return A.to_rows()
    return [list(row) for row in A]


def solve(A, b, *, singularity_tol=1e-12):
    """Solve ``A x = b`` by Gauss-Jordan elimination with partial pivoting.

    ``A`` is a square matrix (Tensor or list of rows), ``b`` a vector.
    Raises :class:`SingularJacobianError` when a pivot is below the
    (scale-relative) tolerance instead of returning a meaningless result.
    """
    M = _as_rows(A)
    n = len(M)
    if any(len(row) != n for row in M):
        raise ValueError("matrix must be square")
    b = [float(v) for v in b]
    if len(b) != n:
        raise ValueError("right-hand side has wrong length")
    scale = max(1.0, max(abs(v) for row in M for v in row))
    M = [row + [b[i]] for i, row in enumerate(M)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(M[r][col]))
        if abs(M[piv][col]) <= singularity_tol * scale:
            raise SingularJacobianError(
                f"singular matrix: pivot {abs(M[piv][col]):.3e} at column {col}")
        M[col], M[piv] = M[piv], M[col]
        inv = 1.0 / M[col][col]
        for r in range(n):
            if r != col:
                factor = M[r][col] * inv
                if factor:
                    for c in range(col, n + 1):
                        M[r][c] -= factor * M[col][c]
    return [M[r][n] / M[r][r] for r in range(n)]


def is_singular(A, *, rel_tol=1e-12):
    """True when Gaussian elimination would hit a (relatively) zero pivot."""
    M = _as_rows(A)
    n = len(M)
    scale = max(1.0, max(abs(v) for row in M for v in row))
    M = [list(row) for row in M]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(M[r][col]))
        if abs(M[piv][col]) <= rel_tol * scale:
            return True
        M[col], M[piv] = M[piv], M[col]
        inv = 1.0 / M[col][col]
        for r in range(col + 1, n):
            factor = M[r][col] * inv
            for c in range(col, n):
                M[r][c] -= factor * M[col][c]
    return False
