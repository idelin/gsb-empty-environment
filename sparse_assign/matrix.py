"""Mutable sparse matrix and immutable historical snapshot views."""

from __future__ import annotations

from typing import Any, Iterable

from .core import (
    ABSENT,
    BadRhsError,
    EMPTY_BLOCK,
    WITHDRAW,
    AssignmentStats,
    MemoryQuota,
    ReadOnlyError,
    RowBlock,
    Selection,
    coalesce,
    is_int,
    leaf,
    normalize_pair,
    run_assign,
    target_points,
)


def _canonical_entries(entries: Iterable[tuple[int, int, Any]]
                       ) -> dict[int, list[tuple[int, Any]]]:
    by_row: dict[int, list[tuple[int, Any]]] = {}
    for entry in entries:
        if (not isinstance(entry, tuple) or len(entry) != 3):
            raise BadRhsError("entries must be (row, col, value) triples")
        r, c, value = entry
        if not is_int(r) or not is_int(c) or not leaf(value):
            raise BadRhsError("entry indices must be ints and value numeric")
        by_row.setdefault(r, []).append((c, value))
    return by_row


class _BaseGrid:
    nrows: int
    ncols: int

    # -- reads -------------------------------------------------------------

    def read_cell(self, r: int, c: int) -> Any:
        block = self._rows[r]
        cols = block.cols
        lo, hi = 0, len(cols)
        while lo < hi:
            mid = (lo + hi) // 2
            if cols[mid] < c:
                lo = mid + 1
            else:
                hi = mid
        if lo < len(cols) and cols[lo] == c:
            return block.vals[lo]
        return ABSENT

    def to_dense(self) -> tuple[tuple[Any, ...], ...]:
        grid = []
        for block in self._rows:
            row = [0] * self.ncols
            for c, value in zip(block.cols, block.vals):
                row[c] = value
            grid.append(tuple(row))
        return tuple(grid)

    @property
    def stored_entries(self) -> int:
        return sum(len(block.cols) for block in self._rows)

    nnz = stored_entries

    # -- selection ---------------------------------------------------------

    def _select(self, row_sel: Any, col_sel: Any,
                structural: bool = False) -> Selection:
        shape, rows, cols, paired, _both = normalize_pair(
            row_sel, col_sel, self.nrows, self.ncols)
        points = target_points(rows, cols, paired)
        values = tuple(self.read_cell(r, c) for r, c in points)
        return Selection(values, shape[0], shape[1], structural)

    def select(self, row_sel: Any, col_sel: Any = None,
               structural: bool = False) -> Selection:
        """Capture an eager pre-assignment snapshot of a region."""

        return self._select(row_sel, col_sel, structural)

    def __getitem__(self, key: Any) -> Any:
        row_sel, col_sel = _split_key(key)
        _, _, _, _, both_scalar = normalize_pair(
            row_sel, col_sel, self.nrows, self.ncols)
        sel = self._select(row_sel, col_sel)
        if both_scalar:
            value = sel.values[0]
            return 0 if value is ABSENT else value
        return sel

    def get(self, r: int, c: int, default: Any = 0) -> Any:
        value = self.read_cell(r, c)
        return default if value is ABSENT else value


class SnapshotView(_BaseGrid):
    """Read-only view of a matrix at one point in history."""

    def __init__(self, nrows: int, ncols: int, rows: tuple[RowBlock, ...],
                 version: int):
        self.nrows = nrows
        self.ncols = ncols
        self._rows = rows
        self.version = version

    @property
    def rows(self) -> tuple[RowBlock, ...]:
        return self._rows

    def snapshot(self) -> "SnapshotView":
        return self

    def assign(self, *args: Any, **kwargs: Any) -> None:
        raise ReadOnlyError("snapshot views are read-only")

    def __setitem__(self, key: Any, value: Any) -> None:
        raise ReadOnlyError("snapshot views are read-only")


class SparseMatrix(_BaseGrid):
    """Canonical sparse matrix with transactional slice assignment."""

    def __init__(self, nrows: int, ncols: int,
                 entries: Iterable[tuple[int, int, Any]] | None = None):
        if not is_int(nrows) or not is_int(ncols) or nrows < 0 or ncols < 0:
            raise ValueError("nrows and ncols must be non-negative integers")
        self.nrows = int(nrows)
        self.ncols = int(ncols)
        self._rows: list[RowBlock] = [EMPTY_BLOCK] * self.nrows
        self._version = 0
        by_row = _canonical_entries(list(entries or ()))
        for r, pairs in by_row.items():
            if not 0 <= r < self.nrows:
                raise IndexError(f"entry row {r} out of range")
            for c, _ in pairs:
                if not 0 <= c < self.ncols:
                    raise IndexError(f"entry col {c} out of range")
        if by_row:
            chosen, _ = coalesce(
                [(r, c) for r, pairs in by_row.items() for c, _ in pairs],
                [v for r, pairs in by_row.items() for _, v in pairs],
            )
            grouped: dict[int, list[tuple[int, Any]]] = {}
            for (r, c), value in chosen.items():
                grouped.setdefault(r, []).append((c, value))
            self._install_built([
                (r, self._build_from_pairs(sorted(pairs)))
                for r, pairs in grouped.items()
            ])

    # -- construction helpers ---------------------------------------------

    def _build_from_pairs(self, pairs: list[tuple[int, Any]]) -> RowBlock:
        pairs.sort(key=lambda pc: pc[0])
        cols: list[int] = []
        vals: list[Any] = []
        for c, value in pairs:
            if cols and cols[-1] == c:
                vals[-1] = value
            else:
                cols.append(c)
                vals.append(value)
        return RowBlock(tuple(cols), tuple(vals))

    def _merge_block(self, old: RowBlock, changes: list[tuple[int, Any]]
                     ) -> RowBlock:
        cols = list(old.cols)
        vals = list(old.vals)
        for c, value in changes:
            lo, hi = 0, len(cols)
            while lo < hi:
                mid = (lo + hi) // 2
                if cols[mid] < c:
                    lo = mid + 1
                else:
                    hi = mid
            if lo < len(cols) and cols[lo] == c:
                if value is WITHDRAW:
                    del cols[lo]
                    del vals[lo]
                else:
                    vals[lo] = value
            elif value is not WITHDRAW:
                cols.insert(lo, c)
                vals.insert(lo, value)
        return RowBlock(tuple(cols), tuple(vals))

    # -- transaction hooks used by run_assign ------------------------------

    def build_row(self, r: int, changes: list[tuple[int, Any]]) -> RowBlock:
        return self._merge_block(self._rows[r], changes)

    def _install_built(self, prepared: list[tuple[int, RowBlock]]) -> None:
        for r, block in prepared:
            self._rows[r] = block

    def install_rows(self, prepared: list[tuple[int, RowBlock]]) -> None:
        self._install_built(prepared)
        self._version += 1

    # -- public mutation ---------------------------------------------------

    def assign(self, row_sel: Any, col_sel: Any, rhs: Any,
               quota: MemoryQuota | None = None) -> AssignmentStats:
        if rhs is self:
            # the matrix as RHS means structural copy of the target region,
            # read eagerly before any row is committed
            rhs = self._select(row_sel, col_sel, structural=True)
        elif isinstance(rhs, SparseMatrix):
            rhs = rhs.select(row_sel, col_sel, structural=True)
        return run_assign(self, row_sel, col_sel, rhs, quota)

    def __setitem__(self, key: Any, rhs: Any) -> None:
        row_sel, col_sel = _split_key(key)
        self.assign(row_sel, col_sel, rhs)

    # -- history -----------------------------------------------------------

    def snapshot(self) -> SnapshotView:
        return SnapshotView(
            self.nrows, self.ncols, tuple(self._rows), self._version)

    @property
    def version(self) -> int:
        return self._version

    # -- invariants --------------------------------------------------------

    def canonical_check(self) -> None:
        """Raise AssertionError if storage violates the canonical contract."""

        assert len(self._rows) == self.nrows
        for r, block in enumerate(self._rows):
            assert isinstance(block, RowBlock)
            assert len(block.cols) == len(block.vals)
            last = -1
            for c, value in zip(block.cols, block.vals):
                assert 0 <= c < self.ncols, (r, c)
                assert c > last, ("unsorted/duplicate column", r, c)
                last = c
                assert value is not WITHDRAW
                assert value is not ABSENT


def _split_key(key: Any) -> tuple[Any, Any]:
    if isinstance(key, tuple):
        if len(key) != 2:
            raise BadRhsError("index must be [row, col]")
        return key
    return key, None
