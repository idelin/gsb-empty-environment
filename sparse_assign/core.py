"""Slice assignment on a small CSR-like sparse matrix.

Public model
------------
* ``SparseMatrix`` stores rows as immutable, canonical row blocks (columns
  strictly increasing, explicit zeros retained).
* ``assign`` runs a validate -> pre-snapshot -> build -> commit transaction;
  failure never leaves a partially updated matrix.
* Selectors may combine ``slice`` and non-contiguous integer sequences.
* Two fancy sequences of equal length pair element-wise (NumPy semantics);
  otherwise selectors form a Cartesian grid.
* Repeated target cells follow "last write wins" in evaluation order.
* RHS sharing storage with the destination is read from the pre-assignment
  snapshot.
* ``WITHDRAW`` deletes a cell; assigning zero keeps an explicit zero.
* A ``MemoryQuota`` rejects the whole assignment before any mutation, and the
  returned stats count the row blocks actually copied.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any, Sequence

ABSENT = object()
WITHDRAW = object()


class SliceAssignmentError(Exception):
    """Base class for all errors raised by this package."""


class IndexBoundaryError(SliceAssignmentError, IndexError):
    """A scalar / fancy index is outside the axis bounds."""


class BadSelectorError(SliceAssignmentError, TypeError):
    """The selector object cannot be interpreted on an axis."""


class ShapeMismatchError(SliceAssignmentError, ValueError):
    """The RHS shape does not broadcast against the target grid."""


class BadRhsError(SliceAssignmentError, TypeError):
    """The right hand side is not a legal value source."""


class ReadOnlyError(SliceAssignmentError):
    """A mutation was attempted on a read-only snapshot view."""


class QuotaExceededError(SliceAssignmentError, MemoryError):
    """The assignment would copy more row blocks than the quota allows."""


@dataclass(frozen=True)
class RowBlock:
    """One canonical sparse row.

    ``cols`` is strictly increasing and ``vals`` runs in parallel; explicit
    zeros stay in ``vals`` instead of being pruned.
    """

    cols: tuple[int, ...]
    vals: tuple[Any, ...]


EMPTY_BLOCK = RowBlock((), ())


def is_int(x: Any) -> bool:
    return isinstance(x, int)


# ---------------------------------------------------------------------------
# Axis / selector normalisation
# ---------------------------------------------------------------------------

def normalize_axis(selector: Any, n: int, axis_name: str) -> tuple[list[int], bool]:
    """Resolve *selector* on an axis of length *n*.

    Returns ``(points, single)``.  Fancy sequences preserve order and
    repeats; slices clip the way Python/NumPy slice assignment clips;
    out-of-range scalar and fancy entries are rejected before any write.
    """

    if selector is None:
        return list(range(n)), False
    if isinstance(selector, slice):
        start, stop, step = selector.start, selector.stop, selector.step
        if step is not None and not is_int(step):
            raise BadSelectorError(f"{axis_name} slice step must be an integer")
        step = 1 if step is None else int(step)
        if step == 0:
            raise BadSelectorError(f"{axis_name} slice step cannot be zero")
        if start is not None:
            if not is_int(start):
                raise BadSelectorError(f"{axis_name} slice start must be an integer")
            start = int(start)
            if start < 0:
                start = max(0, start + n)
            else:
                start = min(start, n)
        if stop is not None:
            if not is_int(stop):
                raise BadSelectorError(f"{axis_name} slice stop must be an integer")
            stop = int(stop)
            if stop < 0:
                stop = max(-1, stop + n)
            else:
                stop = min(stop, n)
        return list(range(n)[slice(start, stop, step)]), False
    if is_int(selector):
        idx = int(selector)
        if idx < -n or idx >= n:
            raise IndexBoundaryError(
                f"{axis_name} index {idx} out of range for size {n}")
        return [idx + n if idx < 0 else idx], True
    if isinstance(selector, (list, tuple, range)):
        points: list[int] = []
        for item in selector:
            if not is_int(item):
                raise BadSelectorError(
                    f"{axis_name} fancy index entries must be integers")
            idx = int(item)
            if idx < -n or idx >= n:
                raise IndexBoundaryError(
                    f"{axis_name} index {idx} out of range for size {n}")
            points.append(idx + n if idx < 0 else idx)
        return points, False
    raise BadSelectorError(
        f"{axis_name} selector is not an int, slice or index sequence")


def normalize_pair(row_sel: Any, col_sel: Any, nrows: int, ncols: int):
    rows, row_single = normalize_axis(row_sel, nrows, "row")
    cols, col_single = normalize_axis(col_sel, ncols, "col")
    paired = (
        isinstance(row_sel, (list, tuple, range))
        and isinstance(col_sel, (list, tuple, range))
        and len(rows) == len(cols)
    )
    if paired:
        shape = (1, len(rows))
    elif row_single:
        shape = (1, len(cols))
    elif col_single:
        shape = (len(rows), 1)
    else:
        shape = (len(rows), len(cols))
    return shape, rows, cols, paired, row_single and col_single


def target_points(rows: Sequence[int], cols: Sequence[int],
                  paired: bool) -> list[tuple[int, int]]:
    """Target cells in row-major evaluation order."""

    if paired:
        return list(zip(rows, cols))
    return [(r, c) for r in rows for c in cols]


# ---------------------------------------------------------------------------
# Nested list / broadcast helpers (no NumPy)
# ---------------------------------------------------------------------------

def leaf(x: Any) -> bool:
    return x is WITHDRAW or isinstance(x, (int, float))


def sequence_shape(seq: Any) -> tuple[int, ...]:
    if not isinstance(seq, (list, tuple)):
        raise BadRhsError("RHS must be a number, nested list of numbers or source")
    if len(seq) == 0:
        return (0,)

    def walk(node: Any) -> tuple[int, ...]:
        if leaf(node):
            return ()
        if not isinstance(node, (list, tuple)):
            raise BadRhsError("RHS contains a non-numeric leaf")
        shapes = [walk(child) for child in node]
        head = shapes[0]
        for shape in shapes[1:]:
            if shape != head:
                raise ShapeMismatchError("RHS has a ragged nested list")
        return (len(node),) + head

    return walk(seq)


def flatten_leaves(seq: Any) -> list[Any]:
    if leaf(seq):
        return [seq]
    out: list[Any] = []
    for child in seq:
        out.extend(flatten_leaves(child))
    return out


def broadcast_compatible(shape: tuple[int, ...], target: tuple[int, ...]) -> bool:
    if len(shape) > len(target):
        return False
    padded = (1,) * (len(target) - len(shape)) + shape
    for dim, tdim in zip(padded, target):
        if dim != 1 and dim != tdim:
            return False
    return True


def rebroadcast(values: Sequence[Any], shape: tuple[int, ...],
                target: tuple[int, int]) -> list[Any]:
    """Lay flat C-order *values* of *shape* out on the *target* grid."""

    total = target[0] * target[1]
    if total == 0:
        return []
    padded = (1,) * (len(target) - len(shape)) + shape
    out: list[Any] = [None] * total
    for i in range(target[0]):
        for j in range(target[1]):
            ii = 0 if padded[0] == 1 else i
            jj = 0 if padded[1] == 1 else j
            out[i * target[1] + j] = values[ii * padded[1] + jj]
    return out


# ---------------------------------------------------------------------------
# Value sources and pinned selections
# ---------------------------------------------------------------------------

class CellSource(ABC):
    """Anything that can feed per-cell values into an assignment."""

    @property
    @abstractmethod
    def shape(self) -> tuple[int, int]:
        raise NotImplementedError

    @abstractmethod
    def flatten(self) -> list[Any]:
        """Row-major values; missing cells read as zero."""
        raise NotImplementedError


@dataclass(frozen=True)
class Selection(CellSource):
    """An eager immutable read captured from a (possibly historical) view.

    Ordinary reads turn missing cells into zero.  ``structural`` selections
    (couriers for a sparse RHS matrix) keep ABSENT so structural zeros are
    not materialised by the assignment engine.
    """

    values: tuple[Any, ...]
    nrows: int
    ncols: int
    structural: bool = False

    @property
    def shape(self) -> tuple[int, int]:
        return (self.nrows, self.ncols)

    def flatten(self) -> list[Any]:
        if self.structural:
            return list(self.values)
        return [0 if v is ABSENT else v for v in self.values]


@dataclass(frozen=True)
class AssignmentStats:
    cells_written: int
    cells_withdrawn: int
    cells_unchanged: int
    repeated_targets: int
    row_blocks_copied: int
    explicit_zeros_written: int


class MemoryQuota:
    """Budget measured in distinct row blocks copied by one assignment."""

    def __init__(self, limit: int):
        if not is_int(limit) or limit < 0:
            raise ValueError("quota limit must be a non-negative integer")
        self.limit = int(limit)
        self.used = 0

    def reserve(self, amount: int) -> None:
        if self.used + amount > self.limit:
            raise QuotaExceededError(
                f"row block budget {self.limit} exceeded "
                f"(need {self.used + amount})")
        self.used += amount


def coalesce(points: Sequence[tuple[int, int]], values: Sequence[Any]
             ) -> tuple[dict[tuple[int, int], Any], int]:
    """Collapse repeated target cells; later evaluation order wins."""

    chosen: dict[tuple[int, int], Any] = {}
    repeated = 0
    for point, value in zip(points, values):
        if point in chosen:
            repeated += 1
        chosen[point] = value
    return chosen, repeated


def is_explicit_zero(value: Any) -> bool:
    return value == 0 and not isinstance(value, bool)


# ---------------------------------------------------------------------------
# Generic transactional assignment engine
# ---------------------------------------------------------------------------

def resolve_values(rhs: Any, target: tuple[int, int]) -> list[Any]:
    """Materialise RHS over *target* before any mutation can occur."""

    if isinstance(rhs, CellSource):
        shape = rhs.shape
        if not broadcast_compatible(shape, target):
            raise ShapeMismatchError(
                f"source shape {shape} cannot target {target}")
        return rebroadcast(rhs.flatten(), shape, target)
    if leaf(rhs):
        return [rhs] * (target[0] * target[1])
    if isinstance(rhs, (list, tuple)):
        shape = sequence_shape(rhs)
        flat = flatten_leaves(rhs)
        if shape == ():
            return rebroadcast(flat, (1, 1), target)
        if not broadcast_compatible(shape, target):
            raise ShapeMismatchError(
                f"RHS shape {shape} cannot target {target}")
        return rebroadcast(flat, shape, target)
    raise BadRhsError(
        "RHS must be a number, nested list of numbers or a cell source")


def run_assign(matrix: Any, row_sel: Any, col_sel: Any, rhs: Any,
               quota: MemoryQuota | None = None) -> AssignmentStats:
    """Validate -> snapshot read -> build rows -> commit, all or nothing."""

    shape, rows, cols, paired, _both_scalar = normalize_pair(
        row_sel, col_sel, matrix.nrows, matrix.ncols)
    points = target_points(rows, cols, paired)
    values = resolve_values(rhs, shape)
    chosen, repeated = coalesce(points, values)

    written = withdrawn = unchanged = explicit_zeros = 0
    per_row: dict[int, list[tuple[int, Any]]] = {}
    for (r, c), value in chosen.items():
        old = matrix.read_cell(r, c)
        if value is ABSENT:
            unchanged += 1
            continue
        if value is WITHDRAW:
            if old is ABSENT:
                unchanged += 1
            else:
                withdrawn += 1
                per_row.setdefault(r, []).append((c, value))
        elif is_explicit_zero(value):
            if old is ABSENT:
                written += 1
                explicit_zeros += 1
                per_row.setdefault(r, []).append((c, value))
            elif old == 0:
                unchanged += 1
            else:
                written += 1
                per_row.setdefault(r, []).append((c, value))
        elif old is value or (old is not ABSENT and old == value):
            unchanged += 1
        else:
            written += 1
            per_row.setdefault(r, []).append((c, value))

    changed_rows = sorted(per_row)
    if quota is not None:
        quota.reserve(len(changed_rows))  # reject before rebuilding blocks

    prepared = [(r, matrix.build_row(r, per_row[r])) for r in changed_rows]
    matrix.install_rows(prepared)  # single commit point
    return AssignmentStats(
        cells_written=written,
        cells_withdrawn=withdrawn,
        cells_unchanged=unchanged,
        repeated_targets=repeated,
        row_blocks_copied=len(prepared),
        explicit_zeros_written=explicit_zeros,
    )
