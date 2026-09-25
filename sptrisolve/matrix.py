"""Sparse triangular matrix with cached, incrementally maintained analysis."""

from __future__ import annotations

from .analysis import SolveAnalysis
from .errors import (DimensionError, MissingPivotError, NotTriangularError,
                     StructureError, ZeroPivotError)

__all__ = ["TriangularMatrix", "TransposeView"]


def _as_index(value, kind, position):
    if isinstance(value, bool) or not isinstance(value, int):
        raise DimensionError(
            f"entry {position}: {kind} index must be an int, got {value!r}")
    return value


def _as_number(value, position):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise StructureError(
            f"entry {position}: value must be a real number, got {value!r}")
    return float(value)


class TriangularMatrix:
    """A sparse triangular matrix with explicit size and indices.

    The dimension ``n``, the triangle (``lower``) and the diagonal convention
    (``unit_diagonal``) are explicit inputs.  Duplicate coordinates are merged
    by summation before any structural check.  Entries outside the declared
    triangle are rejected, never silently ignored.
    """

    def __init__(self, n, *, lower=True, unit_diagonal=False):
        if isinstance(n, bool) or not isinstance(n, int) or n < 0:
            raise DimensionError(f"n must be a non-negative int, got {n!r}")
        self.n = n
        self.lower = bool(lower)
        self.unit_diagonal = bool(unit_diagonal)
        self._off = {}                       # (row, col) -> value, off-diagonal
        self._diag = None if self.unit_diagonal else [None] * n
        self._version = 0
        self._analyses = {False: None, True: None}
        self._stats = {
            "solve_calls": 0,
            "analysis_builds": 0,
            "analysis_reuses": 0,
            "partial_reanalyses": 0,
            "nodes_recomputed": 0,
        }
        self._t_view = None

    # ------------------------------------------------------------------ build
    @classmethod
    def from_coo(cls, n, rows, cols, values, *, lower=True,
                 unit_diagonal=False):
        """Build from COO triplets; duplicate coordinates are summed first."""
        if not (len(rows) == len(cols) == len(values)):
            raise DimensionError(
                "rows, cols and values must have equal lengths, got "
                f"{len(rows)}, {len(cols)}, {len(values)}")
        matrix = cls(n, lower=lower, unit_diagonal=unit_diagonal)
        merged = {}
        for k, (r, c, v) in enumerate(zip(rows, cols, values)):
            r = _as_index(r, "row", k)
            c = _as_index(c, "col", k)
            if not (0 <= r < n and 0 <= c < n):
                raise StructureError(
                    f"entry {k}: index ({r}, {c}) out of range for n={n}")
            if (lower and c > r) or (not lower and r > c):
                raise NotTriangularError(
                    f"entry {k}: ({r}, {c}) lies outside the "
                    f"{'lower' if lower else 'upper'} triangle")
            if r == c and unit_diagonal:
                raise StructureError(
                    f"entry {k}: a unit-diagonal matrix must not store "
                    "diagonal entries")
            key = (r, c)
            merged[key] = merged.get(key, 0.0) + _as_number(v, k)
        for (r, c), v in merged.items():
            if r == c:
                matrix._diag[r] = v
            elif v != 0.0:
                matrix._off[(r, c)] = v
        if not unit_diagonal:
            missing = [i for i in range(n) if matrix._diag[i] is None]
            if missing:
                raise MissingPivotError(
                    f"missing diagonal entries (pivots) at rows {missing}")
        return matrix

    # -------------------------------------------------------------- mutation
    def _check_coordinates(self, r, c):
        for name, v in (("row", r), ("col", c)):
            if isinstance(v, bool) or not isinstance(v, int):
                raise DimensionError(f"{name} index must be an int, got {v!r}")
        if not (0 <= r < self.n and 0 <= c < self.n):
            raise StructureError(
                f"index ({r}, {c}) out of range for n={self.n}")
        if r != c:
            if self.lower and c > r:
                raise NotTriangularError(
                    f"({r}, {c}) lies outside the lower triangle")
            if not self.lower and r > c:
                raise NotTriangularError(
                    f"({r}, {c}) lies outside the upper triangle")

    def set_value(self, r, c, value):
        """Update the numeric value of an existing entry.

        Purely numeric changes keep the compiled analysis valid.  Setting an
        off-diagonal entry to exactly zero removes it, which is a structural
        change and triggers a (partial) re-analysis.
        """
        if r == c:
            self.set_diagonal(r, value)
            return
        self._check_coordinates(r, c)
        if (r, c) not in self._off:
            raise StructureError(
                f"entry ({r}, {c}) does not exist; use insert()")
        value = _as_number(value, f"({r}, {c})")
        if value == 0.0:
            self.remove(r, c)
        else:
            self._off[(r, c)] = value

    def set_diagonal(self, i, value):
        """Set a diagonal value (numeric change; zero fails only at solve)."""
        if self.unit_diagonal:
            raise StructureError("the diagonal of a unit-diagonal matrix is "
                                 "implicitly one and cannot be set")
        if isinstance(i, bool) or not isinstance(i, int):
            raise DimensionError(f"row index must be an int, got {i!r}")
        if not 0 <= i < self.n:
            raise StructureError(f"row {i} out of range for n={self.n}")
        self._diag[i] = _as_number(value, f"({i}, {i})")

    def insert(self, r, c, value):
        """Insert a new off-diagonal entry (structural change).

        Only the affected part of every compiled analysis is invalidated and
        re-analysed; unaffected nodes keep their previously computed levels.
        """
        self._check_coordinates(r, c)
        if r == c:
            raise StructureError("use set_diagonal() for diagonal entries")
        if (r, c) in self._off:
            raise StructureError(
                f"entry ({r}, {c}) already exists; use set_value()")
        value = _as_number(value, f"({r}, {c})")
        if value == 0.0:
            raise StructureError("inserting an explicit zero entry is "
                                 "meaningless and refused")
        self._off[(r, c)] = value
        self._structure_edge_changed(r, c, added=True)

    def remove(self, r, c):
        """Remove an existing off-diagonal entry (structural change)."""
        self._check_coordinates(r, c)
        if (r, c) not in self._off:
            raise StructureError(f"entry ({r}, {c}) does not exist")
        del self._off[(r, c)]
        self._structure_edge_changed(r, c, added=False)

    def _structure_edge_changed(self, r, c, added):
        self._version += 1
        for transpose in (False, True):
            analysis = self._analyses[transpose]
            if analysis is None:
                continue
            dependent, dep = (r, c) if not transpose else (c, r)
            recomputed = analysis.apply_edge_change(dependent, dep, (r, c),
                                                    added)
            self._stats["partial_reanalyses"] += 1
            self._stats["nodes_recomputed"] += recomputed

    # -------------------------------------------------------------- analysis
    def _build_analysis(self, transpose):
        deps = [set() for _ in range(self.n)]
        terms = [[] for _ in range(self.n)]
        for (r, c) in self._off:
            node, dep = (r, c) if not transpose else (c, r)
            deps[node].add(dep)
            terms[node].append((dep, (r, c)))
        return SolveAnalysis(self.n, transpose, deps, terms)

    def analysis(self, *, transpose=False):
        """Return the compiled schedule, building it once and reusing it."""
        if self._analyses[transpose] is None:
            self._analyses[transpose] = self._build_analysis(transpose)
            self._stats["analysis_builds"] += 1
        else:
            self._stats["analysis_reuses"] += 1
        return self._analyses[transpose]

    # ------------------------------------------------------------------ solve
    def _check_vector(self, b):
        try:
            values = list(b)
        except TypeError:
            raise DimensionError(
                f"right-hand side must be a sequence of length {self.n}"
            ) from None
        if len(values) != self.n:
            raise DimensionError(
                f"right-hand side has length {len(values)}, expected {self.n}")
        return [_as_number(v, f"b[{k}]") for k, v in enumerate(values)]

    def solve(self, b, *, transpose=False):
        """Solve ``A x = b`` (or ``A.T x = b`` with ``transpose=True``).

        Any numerical failure raises and no partial solution is published.
        """
        rhs = self._check_vector(b)
        analysis = self.analysis(transpose=transpose)
        self._stats["solve_calls"] += 1
        return self._substitute(rhs, analysis)

    def solve_many(self, rhs_list, *, transpose=False):
        """Solve for multiple right-hand sides sharing one compiled analysis.

        ``rhs_list`` is a sequence of right-hand-side vectors; the result is
        the list of the corresponding solution vectors, in the same order.
        """
        vecs = [self._check_vector(b) for b in rhs_list]
        analysis = self.analysis(transpose=transpose)
        self._stats["solve_calls"] += len(vecs)
        return [self._substitute(v, analysis) for v in vecs]

    def _substitute(self, b, analysis):
        x = [0.0] * self.n
        off = self._off
        diag = self._diag
        unit = self.unit_diagonal
        for level in analysis.levels:
            for node in level:
                acc = b[node]
                for dep, key in analysis.terms[node]:
                    acc -= off[key] * x[dep]
                if unit:
                    x[node] = acc
                else:
                    d = diag[node]
                    if d is None:
                        raise MissingPivotError(
                            f"row {node} has no diagonal entry")
                    if d == 0.0:
                        raise ZeroPivotError(f"zero pivot at row {node}")
                    x[node] = acc / d
        return x

    # ------------------------------------------------------------------ views
    @property
    def T(self):
        """Transpose view: a lower matrix is solved as upper and vice versa."""
        if self._t_view is None:
            self._t_view = TransposeView(self)
        return self._t_view

    # ------------------------------------------------------------------ stats
    def stats(self):
        """Analysis cache statistics, including the analysis reuse rate."""
        report = dict(self._stats)
        total = report["analysis_builds"] + report["analysis_reuses"]
        report["analysis_reuse_rate"] = (
            report["analysis_reuses"] / total if total else 0.0)
        report["structure_version"] = self._version
        return report


class TransposeView:
    """A lightweight transpose view sharing the matrix and its caches.

    Solving through the view treats a lower matrix as upper (and vice versa)
    without copying or rebuilding any data.
    """

    def __init__(self, matrix):
        self._matrix = matrix

    @property
    def n(self):
        return self._matrix.n

    @property
    def lower(self):
        return not self._matrix.lower

    @property
    def unit_diagonal(self):
        return self._matrix.unit_diagonal

    def analysis(self):
        return self._matrix.analysis(transpose=True)

    def solve(self, b):
        return self._matrix.solve(b, transpose=True)

    def solve_many(self, rhs_list):
        return self._matrix.solve_many(rhs_list, transpose=True)
