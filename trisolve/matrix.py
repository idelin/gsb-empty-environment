"""Coordinate (COO) triangular matrix with explicit, validated structure.

The matrix owns its triangle contract:

* dimensions and every index are passed explicitly (0- or 1-based);
* repeated (row, col) coordinates are merged by summing their values before
  any validation happens, so the solver always sees a unique-entry pattern;
* entries outside the declared lower/upper triangle are rejected rather than
  silently dropped;
* a non-unit matrix must have one diagonal entry per row;
* a unit-diagonal matrix must have *no* explicit diagonal entries (the
  implicit unit diagonal is supplied by the solver);
* changing off-diagonal structure bumps a structure token, which lets cached
  symbolic analyses detect that they are stale.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import MissingPivotError, StructureError

LOWER = "L"
UPPER = "U"


@dataclass(frozen=True)
class Entry:
    """A single off-diagonal (or diagonal) entry after coordinate merging."""

    row: int
    col: int
    value: float


class TriangularMatrix:
    """Immutable-by-convention sparse triangular matrix with structure tokens."""

    def __init__(
        self,
        n: int,
        rows,
        cols,
        values,
        *,
        uplo: str = LOWER,
        unit_diagonal: bool = False,
        index_base: int = 0,
    ) -> None:
        if not isinstance(n, int):
            raise StructureError("matrix size n must be an integer")
        if n < 0:
            raise StructureError("matrix size n must be non-negative")
        if uplo not in (LOWER, UPPER):
            raise StructureError(f"uplo must be {LOWER!r} or {UPPER!r}, got {uplo!r}")
        if index_base not in (0, 1):
            raise StructureError("index_base must be 0 or 1")
        rows = list(rows)
        cols = list(cols)
        values = list(values)
        if not (len(rows) == len(cols) == len(values)):
            raise StructureError("rows, cols and values must have equal length")

        self.n = n
        self.uplo = uplo
        self.unit_diagonal = bool(unit_diagonal)
        self._index_base = index_base

        # Normalize indices to the internal 0-based convention and validate.
        coordinate_value: dict[tuple[int, int], float] = {}
        for raw_r, raw_c, raw_v in zip(rows, cols, values):
            if not isinstance(raw_r, int) or not isinstance(raw_c, int):
                raise StructureError("row/column indices must be integers")
            r = raw_r - index_base
            c = raw_c - index_base
            if not (0 <= r < n and 0 <= c < n):
                raise StructureError(
                    f"entry ({raw_r}, {raw_c}) is outside the {n}x{n} matrix "
                    f"(index_base={index_base})"
                )
            if (r, c) in coordinate_value:
                coordinate_value[r, c] += float(raw_v)
            else:
                coordinate_value[r, c] = float(raw_v)

        # Split diagonal and off-diagonal entries and enforce the triangle.
        diag: dict[int, float] = {}
        off: dict[tuple[int, int], float] = {}
        outside: list[tuple[int, int]] = []
        for (r, c), v in coordinate_value.items():
            if r == c:
                diag[r] = v
            else:
                inside = r > c if uplo == LOWER else r < c
                if not inside:
                    outside.append((r + index_base, c + index_base))
                else:
                    off[r, c] = v
        if outside:
            outside.sort()
            preview = ", ".join(f"({r}, {c})" for r, c in outside[:8])
            tail = "" if len(outside) <= 8 else f", ... (+{len(outside) - 8} more)"
            tri_name = "lower" if uplo == LOWER else "upper"
            raise StructureError(
                f"{len(outside)} entr{'y' if len(outside) == 1 else 'ies'} outside "
                f"the declared {tri_name} triangle: {preview}{tail}; the structure "
                "is not triangular and out-of-triangle entries are not ignored"
            )

        if unit_diagonal:
            if diag:
                shown = ", ".join(str(i + index_base) for i in sorted(diag)[:8])
                raise StructureError(
                    "unit-diagonal matrix must not carry explicit diagonal "
                    f"entries; found at rows: {shown}"
                )
        else:
            missing = [i for i in range(n) if i not in diag]
            if missing:
                raise MissingPivotError([i + index_base for i in missing])

        self._diag = dict(sorted(diag.items()))
        # Sorting off-diagonal coordinates gives deterministic evidence.
        self._off = {coord: off[coord] for coord in sorted(off)}
        self._token = self._make_token()

    # ------------------------------------------------------------------ state

    def _make_token(self) -> tuple:
        # Identity-like signature: only the structure participates, never
        # the numeric values, so value-only updates reuse the analysis.
        return (
            self.n,
            self.uplo,
            self.unit_diagonal,
            tuple(sorted(self._off)),
        )

    @property
    def structure_token(self) -> tuple:
        """Opaque hashable signature of the current symbolic structure."""
        return self._token

    @property
    def entries(self) -> list[Entry]:
        """All entries (diagonal first) with merged coordinates, sorted."""
        out = [Entry(r, r, v) for r, v in self._diag.items()]
        out.extend(Entry(r, c, v) for (r, c), v in self._off.items())
        return out

    @property
    def off_diagonal(self) -> list[Entry]:
        return [Entry(r, c, v) for (r, c), v in self._off.items()]

    def diagonal_value(self, row: int) -> float:
        """Stored diagonal value (``1.0`` implicitly for a unit diagonal)."""
        if not 0 <= row < self.n:
            raise StructureError(f"row {row} out of range")
        return 1.0 if self.unit_diagonal else self._diag[row]

    def value(self, row: int, col: int) -> float:
        """Return the stored value, or 0.0 for a structural zero."""
        if row == col:
            return self.diagonal_value(row)
        return self._off.get((row, col), 0.0)

    # ------------------------------------------------------------- mutation

    def _bump_structure(self) -> None:
        self._token = self._make_token()

    def set_value(self, row: int, col: int, value: float) -> None:
        """Change the numeric value of an *existing* structural entry.

        Adding or removing structure is intentionally not possible here;
        use :meth:`add_entry` so that structure-dependent caches invalidate.
        """
        if not 0 <= row < self.n or not 0 <= col < self.n:
            raise StructureError(f"entry ({row}, {col}) out of range (0-based)")
        if row == col:
            if self.unit_diagonal:
                raise StructureError("cannot set an explicit unit diagonal")
            self._diag[row] = float(value)
        else:
            if (row, col) not in self._off:
                raise StructureError(
                    f"({row}, {col}) is not an existing entry; use add_entry()"
                )
            self._off[row, col] = float(value)
        # Values never enter the token: cached symbolic analysis stays valid.

    def add_entry(self, row: int, col: int, value: float) -> None:
        """Add an off-diagonal entry and invalidate analyses of this pattern."""
        if not 0 <= row < self.n or not 0 <= col < self.n:
            raise StructureError(f"entry ({row}, {col}) out of range (0-based)")
        if row == col:
            raise StructureError(
                "pivot structure is fixed at construction; cannot add a diagonal"
            )
        inside = row > col if self.uplo == LOWER else row < col
        if not inside:
            raise StructureError(
                f"entry ({row}, {col}) is outside the "
                f"{'lower' if self.uplo == LOWER else 'upper'} triangle"
            )
        if (row, col) in self._off:
            raise StructureError(f"entry ({row}, {col}) already exists; use set_value()")
        self._off[row, col] = float(value)
        self._off = {coord: self._off[coord] for coord in sorted(self._off)}
        self._bump_structure()

    def remove_entry(self, row: int, col: int) -> None:
        """Remove an off-diagonal entry and invalidate analyses of this pattern."""
        if row == col:
            raise StructureError("cannot remove a pivot position after construction")
        if (row, col) not in self._off:
            raise StructureError(f"entry ({row}, {col}) is not present")
        del self._off[row, col]
        self._bump_structure()

    def pattern_signature(self, index_base: int = 0) -> str:
        """Human-readable structure summary (useful for demo output)."""
        parts = [f"{r + index_base},{c + index_base}" for (r, c) in sorted(self._off)]
        return f"n={self.n} uplo={self.uplo} off=[{' '.join(parts)}]"
