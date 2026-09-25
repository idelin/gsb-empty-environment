"""Small hand-written dense helpers (no NumPy/SciPy).

Used only to cross-check the sparse implementation on tiny chains.
"""

from __future__ import annotations

from typing import List, Sequence, Tuple


def dense_matvec(mat: Sequence[Sequence[float]],
                 vec: Sequence[float]) -> List[float]:
    rows = len(mat)
    cols = len(mat[0]) if rows else 0
    if len(vec) != cols:
        raise ValueError("dense_matvec dimension mismatch")
    return [sum(mat[i][j] * vec[j] for j in range(cols)) for i in range(rows)]


def dense_matmul(a: Sequence[Sequence[float]],
                 b: Sequence[Sequence[float]]) -> List[List[float]]:
    if not a or not b:
        raise ValueError("empty matrix in dense_matmul")
    m, k, n = len(a), len(b), len(b[0])
    if any(len(row) != k for row in a) or any(len(row) != n for row in b):
        raise ValueError("dense_matmul dimension mismatch")
    out = [[0.0 for _ in range(n)] for _ in range(m)]
    for i in range(m):
        for t in range(k):
            air = a[i][t]
            for j in range(n):
                out[i][j] += air * b[t][j]
    return out


def dense_abs_matvec(mat: Sequence[Sequence[float]],
                     vec: Sequence[float]) -> List[float]:
    cols = len(mat[0])
    return [sum(abs(mat[i][j]) * vec[j] for j in range(cols))
            for i in range(len(mat))]


def sparse_to_dense(mat) -> List[List[float]]:
    out = [[0.0 for _ in range(mat.cols)] for _ in range(mat.rows)]
    for (r, c), v in mat.entries.items():
        out[r][c] = v
    return out
