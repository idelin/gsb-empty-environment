"""Dense snapshot reference model used to cross-check the sparse matrix."""

from __future__ import annotations

from typing import Any, Iterable

from .core import (
    ABSENT,
    BadRhsError,
    WITHDRAW,
    AssignmentStats,
    MemoryQuota,
    Selection,
    is_int,
    leaf,
    normalize_pair,
    run_assign,
    target_points,
)
from .matrix import SparseMatrix, _split_key


class DenseReference:
    """Small fully-dense matrix with ABSENT markers for structural zeros.

    It executes exactly the same assignment engine as ``SparseMatrix``, so
    randomized overlapping assignments can be compared cell by cell.
    """

    def __init__(self, nrows: int, ncols: int,
                 entries: Iterable[tuple[int, int, Any]] | None = None):
        if not is_int(nrows) or not is_int(ncols) or nrows < 0 or ncols < 0:
            raise ValueError("nrows and ncols must be non-negative integers")
        self.nrows = int(nrows)
        self.ncols = int(ncols)
        self._cells: list[list[Any]] = [
            [ABSENT] * self.ncols for _ in range(self.nrows)
        ]
        for entry in entries or ():
            if (not isinstance(entry, tuple) or len(entry) != 3):
                raise BadRhsError("entries must be (row, col, value) triples")
            r, c, value = entry
            if not 0 <= r < self.nrows or not 0 <= c < self.ncols:
                raise IndexError("entry index out of range")
            if not leaf(value) or value is WITHDRAW:
                raise BadRhsError("initial entries must be numeric")
            self._cells[r][c] = value

    # -- engine hooks ------------------------------------------------------

    def read_cell(self, r: int, c: int) -> Any:
        return self._cells[r][c]

    def build_row(self, r: int, changes: list[tuple[int, Any]]) -> list[Any]:
        row = list(self._cells[r])  # copy the row block first
        for c, value in changes:
            row[c] = ABSENT if value is WITHDRAW else value
        return row

    def install_rows(self, prepared: list[tuple[int, list[Any]]]) -> None:
        for r, row in prepared:
            self._cells[r] = row

    # -- API mirroring SparseMatrix ---------------------------------------

    def assign(self, row_sel: Any, col_sel: Any, rhs: Any,
               quota: MemoryQuota | None = None) -> AssignmentStats:
        if rhs is self:
            rhs = self.select(row_sel, col_sel)  # pre-assignment region read
        elif isinstance(rhs, SparseMatrix):
            # cross-model RHS: structural courier keeps structural zeros
            rhs = rhs.select(row_sel, col_sel, structural=True)
        return run_assign(self, row_sel, col_sel, rhs, quota)

    def __setitem__(self, key: Any, rhs: Any) -> None:
        row_sel, col_sel = _split_key(key)
        self.assign(row_sel, col_sel, rhs)

    def select(self, row_sel: Any, col_sel: Any = None) -> Selection:
        shape, rows, cols, paired, _both = normalize_pair(
            row_sel, col_sel, self.nrows, self.ncols)
        points = target_points(rows, cols, paired)
        values = tuple(
            0 if self.read_cell(r, c) is ABSENT else self.read_cell(r, c)
            for r, c in points
        )
        return Selection(values, shape[0], shape[1])

    def __getitem__(self, key: Any) -> Any:
        row_sel, col_sel = _split_key(key)
        _, _, _, _, both_scalar = normalize_pair(
            row_sel, col_sel, self.nrows, self.ncols)
        sel = self.select(row_sel, col_sel)
        if both_scalar:
            value = sel.values[0]
            return 0 if value is ABSENT else value
        return sel

    def get(self, r: int, c: int, default: Any = 0) -> Any:
        value = self._cells[r][c]
        return default if value is ABSENT else value

    # -- comparison helpers ------------------------------------------------

    def to_dense(self) -> tuple[tuple[Any, ...], ...]:
        return tuple(
            tuple(0 if value is ABSENT else value for value in row)
            for row in self._cells
        )

    def presence_grid(self) -> tuple[tuple[bool, ...], ...]:
        return tuple(
            tuple(value is not ABSENT for value in row) for row in self._cells
        )

    def clone(self) -> "DenseReference":
        other = DenseReference(self.nrows, self.ncols)
        other._cells = [list(row) for row in self._cells]
        return other

    @property
    def stored_entries(self) -> int:
        return sum(value is not ABSENT for row in self._cells for value in row)
