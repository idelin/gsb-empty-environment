"""Sparse matrices with explicit sizes and indices (standard library only).

Sizes and indices are always explicit inputs: the caller provides
``n_rows``, ``n_cols`` and the ``(row, col, value)`` triples. Duplicate
coordinates are summed. ``version`` identifies the matrix data so that
solvers can refuse to mix Krylov state (search directions) across
different matrix versions.
"""


class MatrixFormatError(ValueError):
    """Raised when explicit sizes/indices are inconsistent."""


class DimensionError(ValueError):
    """Raised when operand dimensions do not match."""


class SymmetryReport:
    """Numeric evidence about the (a)symmetry of a matrix."""

    __slots__ = (
        "square",
        "max_abs_asymmetry",
        "row",
        "col",
        "value_ij",
        "value_ji",
        "entries_checked",
    )

    def __init__(self, square, max_abs_asymmetry, row, col, value_ij, value_ji, entries_checked):
        self.square = square
        self.max_abs_asymmetry = max_abs_asymmetry
        self.row = row
        self.col = col
        self.value_ij = value_ij
        self.value_ji = value_ji
        self.entries_checked = entries_checked

    def to_dict(self):
        return {
            "square": self.square,
            "max_abs_asymmetry": self.max_abs_asymmetry,
            "row": self.row,
            "col": self.col,
            "value_ij": self.value_ij,
            "value_ji": self.value_ji,
            "entries_checked": self.entries_checked,
        }

    def __repr__(self):
        return (
            f"SymmetryReport(square={self.square}, "
            f"max_abs_asymmetry={self.max_abs_asymmetry!r}, "
            f"at=({self.row}, {self.col}))"
        )


class SparseMatrix:
    """Sparse matrix in CSR form built from explicit coordinate input."""

    __slots__ = ("n_rows", "n_cols", "version", "_row_ptr", "_col_idx", "_data", "_acc")

    def __init__(self, n_rows, n_cols, rows, cols, values, version=0):
        if not isinstance(n_rows, int) or not isinstance(n_cols, int):
            raise MatrixFormatError("n_rows and n_cols must be ints")
        if n_rows <= 0 or n_cols <= 0:
            raise MatrixFormatError("matrix dimensions must be positive")
        rows = list(rows)
        cols = list(cols)
        values = [float(v) for v in values]
        if not (len(rows) == len(cols) == len(values)):
            raise MatrixFormatError(
                f"rows/cols/values length mismatch: {len(rows)}/{len(cols)}/{len(values)}"
            )
        acc = {}
        for i, j, v in zip(rows, cols, values):
            if not (0 <= i < n_rows) or not (0 <= j < n_cols):
                raise MatrixFormatError(
                    f"index ({i}, {j}) out of range for a {n_rows}x{n_cols} matrix"
                )
            key = (i, j)
            acc[key] = acc.get(key, 0.0) + v
        buckets = [[] for _ in range(n_rows)]
        for (i, j), v in acc.items():
            buckets[i].append((j, v))
        row_ptr = [0]
        col_idx = []
        data = []
        for i in range(n_rows):
            for j, v in sorted(buckets[i]):
                col_idx.append(j)
                data.append(v)
            row_ptr.append(len(col_idx))
        self.n_rows = n_rows
        self.n_cols = n_cols
        self.version = version
        self._row_ptr = row_ptr
        self._col_idx = col_idx
        self._data = data
        self._acc = acc

    @property
    def nnz(self):
        return len(self._data)

    def entries(self):
        """Return the summed coordinate entries as (row, col, value) triples."""
        return [(i, j, v) for (i, j), v in sorted(self._acc.items())]

    def matvec(self, x):
        if len(x) != self.n_cols:
            raise DimensionError(
                f"matvec dimension mismatch: matrix has {self.n_cols} columns, "
                f"vector has length {len(x)}"
            )
        y = [0.0] * self.n_rows
        row_ptr = self._row_ptr
        col_idx = self._col_idx
        data = self._data
        for i in range(self.n_rows):
            s = 0.0
            for t in range(row_ptr[i], row_ptr[i + 1]):
                s += data[t] * x[col_idx[t]]
            y[i] = s
        return y

    def diagonal(self):
        n = min(self.n_rows, self.n_cols)
        d = [0.0] * n
        for (i, j), v in self._acc.items():
            if i == j:
                d[i] = v
        return d

    def symmetry_evidence(self):
        """Return a SymmetryReport with the worst |a_ij - a_ji| found."""
        if self.n_rows != self.n_cols:
            return SymmetryReport(False, None, None, None, None, None, 0)
        max_asym = 0.0
        arg = (None, None, None, None)
        for (i, j), vij in self._acc.items():
            vji = self._acc.get((j, i), 0.0)
            a = abs(vij - vji)
            if a > max_asym:
                max_asym = a
                arg = (i, j, vij, vji)
        return SymmetryReport(True, max_asym, arg[0], arg[1], arg[2], arg[3], len(self._acc))
