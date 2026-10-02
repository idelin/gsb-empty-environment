"""Small dense helpers (standard library only) used to verify the sparse CG.

These are O(n^3) reference routines intended for small systems in tests.
"""


def dense_from_sparse(matrix):
    a = [[0.0] * matrix.n_cols for _ in range(matrix.n_rows)]
    for i, j, v in matrix.entries():
        a[i][j] += v
    return a


def solve_dense(a, b):
    """Gaussian elimination with partial pivoting. Raises on a zero pivot."""
    n = len(a)
    if any(len(row) != n for row in a):
        raise ValueError("solve_dense requires a square matrix")
    if len(b) != n:
        raise ValueError("dimension mismatch between matrix and right-hand side")
    aug = [list(row) + [float(bi)] for row, bi in zip(a, b)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(aug[r][col]))
        if abs(aug[piv][col]) == 0.0:
            raise ValueError(f"singular matrix: zero pivot at column {col}")
        if piv != col:
            aug[col], aug[piv] = aug[piv], aug[col]
        pivot_row = aug[col]
        pivot = pivot_row[col]
        for r in range(col + 1, n):
            factor = aug[r][col] / pivot
            if factor == 0.0:
                continue
            row_r = aug[r]
            for c in range(col, n + 1):
                row_r[c] -= factor * pivot_row[c]
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        s = aug[i][n]
        row = aug[i]
        for j in range(i + 1, n):
            s -= row[j] * x[j]
        x[i] = s / row[i]
    return x
