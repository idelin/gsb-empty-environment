"""Transactional sparse matrix with CSR storage, slice assignment and history views.

Pure standard library, Python 3.11+. Sizes and indices are always explicit;
no NumPy/SciPy is used.

Public semantics
----------------
- Canonical CSR format: column indices strictly sorted inside every row,
  explicit zeros are stored and preserved (never pruned).
- Slice assignment ``mat[rows, cols] = values`` is atomic: any failure
  (out-of-bounds index, shape mismatch, memory quota) raises before a
  single byte of the matrix is mutated.
- Non-contiguous row/column index lists are supported; duplicate targets
  are applied in row-major order so the last write wins.
- Scalar values broadcast over the whole target block.
- The right-hand side is materialized as a dense snapshot *before* the
  assignment starts, so overlapping source/target reads see the
  pre-assignment state.
- Memory quota is expressed in row blocks: an assignment that would copy
  more distinct rows than the quota allows raises ``QuotaExceededError``
  without partial writes. The number of actually copied row blocks is
  reported via ``rows_copied_last`` / ``rows_copied_total``.
- Every committed assignment appends an immutable history version;
  ``history_view(i)`` returns a read-only view of version ``i``.
- ``transaction()`` groups several assignments into one atomic unit that
  rolls back completely on failure.
"""

from __future__ import annotations

from contextlib import contextmanager


class QuotaExceededError(RuntimeError):
    """Assignment would copy more row blocks than the memory quota allows."""


class ShapeMismatchError(ValueError):
    """Value grid does not match the selected target block."""


def _normalize_indices(index, size, axis_name):
    """Turn a slice / int / iterable of ints into an explicit index list.

    Negative indices wrap around like normal Python indexing. Out-of-bounds
    indices raise ``IndexError`` immediately (i.e. before any write).
    """
    if isinstance(index, slice):
        return list(range(*index.indices(size)))
    if isinstance(index, int):
        index = [index]
    result = []
    for value in index:
        if not isinstance(value, int):
            raise TypeError(
                f"{axis_name} index must be int, got {type(value).__name__}")
        if value < 0:
            value += size
        if value < 0 or value >= size:
            raise IndexError(
                f"{axis_name} index {value} out of bounds for size {size}")
        result.append(value)
    return result


def _dense_from_csr(indptr, indices, data, nrows, ncols):
    dense = [[0] * ncols for _ in range(nrows)]
    for row in range(nrows):
        start, end = indptr[row], indptr[row + 1]
        for col, val in zip(indices[start:end], data[start:end]):
            dense[row][col] = val
    return dense


class DenseSnapshot:
    """Immutable dense materialization of a (sub-)matrix.

    Produced by ``SparseMatrix.__getitem__``; because it is fully
    materialized before any assignment starts, it provides the
    pre-assignment snapshot semantics for overlapping reads.
    """

    __slots__ = ("_rows",)

    def __init__(self, rows):
        self._rows = tuple(tuple(row) for row in rows)

    @property
    def shape(self):
        return (len(self._rows), len(self._rows[0]) if self._rows else 0)

    def tolist(self):
        return [list(row) for row in self._rows]

    def __getitem__(self, key):
        return self._rows[key]

    def __iter__(self):
        return iter(self._rows)

    def __eq__(self, other):
        if isinstance(other, DenseSnapshot):
            return self._rows == other._rows
        if isinstance(other, list):
            return self.tolist() == other
        return NotImplemented

    def __repr__(self):
        return f"DenseSnapshot({self.tolist()!r})"


class _CsrReader:
    """Shared read-only behaviour over CSR arrays."""

    _indptr: list | tuple
    _indices: list | tuple
    _data: list | tuple
    _nrows: int
    _ncols: int

    @property
    def shape(self):
        return (self._nrows, self._ncols)

    @property
    def nnz(self):
        return len(self._data)

    def _row_entries(self, row):
        start, end = self._indptr[row], self._indptr[row + 1]
        return self._indices[start:end], self._data[start:end]

    def is_canonical(self):
        """True when every row's column indices are strictly increasing."""
        for row in range(self._nrows):
            cols, _ = self._row_entries(row)
            if any(a >= b for a, b in zip(cols, cols[1:])):
                return False
        return True

    def explicit_zero_count(self):
        return sum(1 for value in self._data if value == 0)

    def to_dense(self):
        return _dense_from_csr(
            self._indptr, self._indices, self._data, self._nrows, self._ncols)

    def _normalize_key(self, key):
        if not isinstance(key, tuple) or len(key) != 2:
            raise TypeError("key must be a (rows, cols) tuple")
        rows = _normalize_indices(key[0], self._nrows, "row")
        cols = _normalize_indices(key[1], self._ncols, "column")
        return rows, cols

    def __getitem__(self, key):
        rows, cols = self._normalize_key(key)
        block = []
        for row in rows:
            old_cols, old_vals = self._row_entries(row)
            row_map = dict(zip(old_cols, old_vals))
            block.append([row_map.get(col, 0) for col in cols])
        return DenseSnapshot(block)


class ReadOnlyView(_CsrReader):
    """Immutable view of one committed history version."""

    __slots__ = ("_indptr", "_indices", "_data", "_nrows", "_ncols", "version")

    def __init__(self, snapshot, nrows, ncols, version):
        self._indptr, self._indices, self._data = snapshot
        self._nrows = nrows
        self._ncols = ncols
        self.version = version

    def __setitem__(self, key, values):
        raise TypeError("history views are read-only")

    def assign(self, *args, **kwargs):
        raise TypeError("history views are read-only")

    def transaction(self):
        raise TypeError("history views are read-only")

    def __repr__(self):
        return (f"ReadOnlyView(version={self.version}, "
                f"shape={self.shape}, nnz={self.nnz})")


class SparseMatrix(_CsrReader):
    """Mutable CSR sparse matrix with transactional slice assignment."""

    def __init__(self, nrows, ncols, entries=(), memory_quota=None):
        if nrows < 0 or ncols < 0:
            raise ValueError("shape must be non-negative")
        if memory_quota is not None and memory_quota < 0:
            raise ValueError("memory quota must be non-negative")
        self._nrows = nrows
        self._ncols = ncols
        self._indptr = [0] * (nrows + 1)
        self._indices = []
        self._data = []
        self._quota = memory_quota
        self._versions = []
        self._txn_depth = 0
        self._txn_changed = False
        self.rows_copied_last = 0
        self.rows_copied_total = 0
        if entries:
            per_row = [dict() for _ in range(nrows)]
            for row, col, value in entries:
                if not 0 <= row < nrows:
                    raise IndexError(f"row index {row} out of bounds")
                if not 0 <= col < ncols:
                    raise IndexError(f"column index {col} out of bounds")
                per_row[row][col] = value  # duplicate entries: last wins
            indices, data, indptr = [], [], [0]
            for row in range(nrows):
                for col in sorted(per_row[row]):
                    indices.append(col)
                    data.append(per_row[row][col])
                indptr.append(len(indices))
            self._indptr, self._indices, self._data = indptr, indices, data
        self._versions.append(self._snapshot())

    # -- snapshots / history -------------------------------------------------

    def _snapshot(self):
        return (tuple(self._indptr), tuple(self._indices), tuple(self._data))

    def _restore(self, snapshot):
        indptr, indices, data = snapshot
        self._indptr, self._indices, self._data = (
            list(indptr), list(indices), list(data))

    @property
    def version_count(self):
        return len(self._versions)

    def history_view(self, version):
        """Read-only view of a committed version (0 = initial state)."""
        if not isinstance(version, int):
            raise TypeError("version must be an int")
        if version < 0:
            version += len(self._versions)
        if not 0 <= version < len(self._versions):
            raise IndexError(f"no history version {version}")
        return ReadOnlyView(
            self._versions[version], self._nrows, self._ncols, version)

    # -- assignment ------------------------------------------------------------

    @staticmethod
    def _normalize_values(values, nrows, ncols):
        if isinstance(values, DenseSnapshot):
            grid = values.tolist()
        elif isinstance(values, (int, float, complex)):
            return [[values] * ncols for _ in range(nrows)]  # broadcast
        else:
            grid = [list(row) for row in values]
        if len(grid) != nrows or any(len(row) != ncols for row in grid):
            raise ShapeMismatchError(
                f"values shape does not match target block {nrows}x{ncols}")
        return grid

    def assign(self, rows, cols, values):
        """Atomically assign ``values`` to the ``rows`` x ``cols`` block.

        Raises before mutating anything on: out-of-bounds indices, value
        shape mismatch, or memory quota exhaustion.
        """
        rows = _normalize_indices(rows, self._nrows, "row")
        cols = _normalize_indices(cols, self._ncols, "column")
        grid = self._normalize_values(values, len(rows), len(cols))

        # Group targets per row, applying in row-major order so that
        # duplicate targets resolve to the last write.
        updates_by_row = {}
        for i, row in enumerate(rows):
            row_updates = updates_by_row.setdefault(row, {})
            for j, col in enumerate(cols):
                row_updates[col] = grid[i][j]
        if not updates_by_row:
            return  # empty selection: no-op, no history entry

        needed = len(updates_by_row)
        if self._quota is not None and needed > self._quota:
            raise QuotaExceededError(
                f"assignment would copy {needed} row blocks, "
                f"quota allows {self._quota}")

        # Build replacement rows off to the side; the live arrays are not
        # touched yet, so any failure above leaves the matrix unchanged.
        rebuilt = {}
        for row, updates in updates_by_row.items():
            old_cols, old_vals = self._row_entries(row)
            merged = dict(zip(old_cols, old_vals))
            merged.update(updates)
            ordered = sorted(merged)
            rebuilt[row] = (ordered, [merged[col] for col in ordered])

        self._apply_rebuilt_rows(rebuilt)
        self.rows_copied_last = needed
        self.rows_copied_total += needed
        if self._txn_depth:
            self._txn_changed = True
        else:
            self._versions.append(self._snapshot())

    def _apply_rebuilt_rows(self, rebuilt):
        indptr, indices, data = [0], [], []
        for row in range(self._nrows):
            if row in rebuilt:
                cols, vals = rebuilt[row]
            else:
                start, end = self._indptr[row], self._indptr[row + 1]
                cols = self._indices[start:end]
                vals = self._data[start:end]
            indices.extend(cols)
            data.extend(vals)
            indptr.append(len(indices))
        self._indptr, self._indices, self._data = indptr, indices, data

    def __setitem__(self, key, values):
        rows, cols = self._normalize_key(key)
        self.assign(rows, cols, values)

    # -- transactions ----------------------------------------------------------

    @contextmanager
    def transaction(self):
        """Group assignments into one atomic unit with a single history entry.

        On any exception the matrix, statistics and history are restored to
        their pre-transaction state.
        """
        if self._txn_depth:
            raise RuntimeError("nested transactions are not supported")
        saved = self._snapshot()
        saved_stats = (self.rows_copied_last, self.rows_copied_total)
        self._txn_depth = 1
        self._txn_changed = False
        try:
            yield self
        except Exception:
            self._restore(saved)
            self.rows_copied_last, self.rows_copied_total = saved_stats
            raise
        else:
            if self._txn_changed:
                self._versions.append(self._snapshot())
        finally:
            self._txn_depth = 0
            self._txn_changed = False

    def __repr__(self):
        return (f"SparseMatrix(shape={self.shape}, nnz={self.nnz}, "
                f"versions={self.version_count})")
