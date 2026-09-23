"""Piecewise rational functions on a shared, automatically refined partition.

The real line is split at every rational endpoint and every rational domain
exclusion (pole / removable hole) of every piece expression.  Between two
consecutive boundaries a *segment cell* holds the set of contributing pieces;
at each boundary a *point cell* does the same.  This mapping from cells to
their source pieces is the provenance (来源映射) and is maintained
incrementally when pieces are added, replaced or removed: only the cells
touched by the edit are split, merged or re-tagged.

Coalescing rule (per the specification): two adjacent pieces are merged
only when the shared boundary point is itself defined — identical
expressions on both sides of an excluded point are never merged across it.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .interval import Interval
from .rational import RationalFunc


@dataclass
class Piece:
    interval: Interval
    expr: RationalFunc
    source: str
    pid: int


@dataclass
class PointCell:
    value: Fraction
    cid: int
    contributors: tuple[int, ...] = ()


@dataclass
class SegCell:
    lo: Fraction | None
    hi: Fraction | None
    cid: int
    contributors: tuple[int, ...] = ()

    def interval(self) -> Interval:
        return Interval(self.lo, self.hi, False, False)


def _fold_exprs(exprs: list[RationalFunc]) -> RationalFunc | None:
    """Sum the contributing expressions (overlap semantics: addition)."""
    if not exprs:
        return None
    acc = exprs[0]
    for nxt in exprs[1:]:
        acc = acc + nxt
    return acc


class PiecewiseModel:
    """A piecewise rational function with incremental partition maintenance."""

    def __init__(self, name: str = "f"):
        self.name = name
        self.pieces: dict[int, Piece] = {}
        self.boundaries: list[Fraction] = []
        self.points: list[PointCell] = []
        self.segs: list[SegCell] = [SegCell(None, None, 0, ())]
        self._boundary_sources: dict[Fraction, set[int]] = {}
        self._next_pid = 1
        self._next_cid = 1
        self.edits: list[str] = []

    # ------------------------------------------------------------------ edits
    def set_piece(self, interval: Interval, expr: RationalFunc, source: str) -> int:
        """Add a piece; split the shared partition at its critical points."""
        pid = self._next_pid
        self._next_pid += 1
        piece = Piece(interval, expr, source, pid)
        self.pieces[pid] = piece
        new_bounds = self._piece_boundaries(piece)
        existing = self._boundary_index()
        added = sorted(new_bounds - existing)
        for b in new_bounds:
            self._boundary_sources.setdefault(b, set()).add(pid)
        for b in added:
            self._split_at(b)
        touched = self._tag_contributor(pid, interval)
        self.boundaries = sorted(self._boundary_sources)
        self.edits.append(
            f"set_piece[{source}] pid={pid} on {interval.to_human()}: "
            f"新增边界 {len(added)} 个，更新 {touched} 个单元"
        )
        return pid

    def remove_piece(self, pid: int) -> None:
        """Remove a piece; garbage-collect boundaries no piece still needs."""
        piece = self.pieces.pop(pid)
        self._untag_contributor(pid)
        for b in self._piece_boundaries(piece):
            sources = self._boundary_sources.get(b)
            if sources is None:
                continue
            sources.discard(pid)
            if not sources:
                if self._merge_at(b):
                    del self._boundary_sources[b]
        self.boundaries = sorted(self._boundary_sources)
        self.edits.append(f"remove_piece pid={pid} ({piece.source})")

    def replace_piece(self, pid: int, interval: Interval,
                      expr: RationalFunc, source: str) -> int:
        """Edit a piece in place; only affected cells are re-tagged."""
        self.remove_piece(pid)
        return self.set_piece(interval, expr, source)

    # ------------------------------------------------------- partition upkeep
    def _piece_boundaries(self, piece: Piece) -> set[Fraction]:
        out: set[Fraction] = set()
        if piece.interval.lo is not None:
            out.add(piece.interval.lo)
        if piece.interval.hi is not None:
            out.add(piece.interval.hi)
        for p in piece.expr.rational_exclusions():
            if piece.interval.lo is None or p >= piece.interval.lo:
                if piece.interval.hi is None or p <= piece.interval.hi:
                    out.add(p)
        return out

    def _boundary_index(self) -> set[Fraction]:
        return set(self._boundary_sources)

    def _split_at(self, b: Fraction) -> None:
        for i, seg in enumerate(self.segs):
            if (seg.lo is None or seg.lo < b) and (seg.hi is None or b < seg.hi):
                left = SegCell(seg.lo, b, self._fresh_cid(), seg.contributors)
                right = SegCell(b, seg.hi, self._fresh_cid(), seg.contributors)
                point = PointCell(b, self._fresh_cid(), seg.contributors)
                self.segs[i:i + 1] = [left, right]
                self.points.insert(i, point)
                return
        raise AssertionError(f"boundary {b} not inside any segment")

    def _merge_at(self, b: Fraction) -> None:
        for i, point in enumerate(self.points):
            if point.value == b:
                left, right = self.segs[i], self.segs[i + 1]
                if left.contributors == right.contributors:
                    merged = SegCell(left.lo, right.hi, self._fresh_cid(),
                                     left.contributors)
                    self.segs[i:i + 2] = [merged]
                    self.points.pop(i)
                    return True
                return False
        return False

    def _tag_contributor(self, pid: int, interval: Interval) -> int:
        touched = 0
        for i, point in enumerate(self.points):
            if interval.contains(point.value):
                self.points[i] = PointCell(point.value, point.cid,
                                           point.contributors + (pid,))
                touched += 1
        for i, seg in enumerate(self.segs):
            lo_ok = interval.lo is None or seg.hi is None or interval.lo < seg.hi
            hi_ok = interval.hi is None or seg.lo is None or seg.lo < interval.hi
            if lo_ok and hi_ok:
                self.segs[i] = SegCell(seg.lo, seg.hi, seg.cid,
                                       seg.contributors + (pid,))
                touched += 1
        return touched

    def _untag_contributor(self, pid: int) -> None:
        for i, point in enumerate(self.points):
            if pid in point.contributors:
                self.points[i] = PointCell(
                    point.value, point.cid,
                    tuple(p for p in point.contributors if p != pid))
        for i, seg in enumerate(self.segs):
            if pid in seg.contributors:
                self.segs[i] = SegCell(
                    seg.lo, seg.hi, seg.cid,
                    tuple(p for p in seg.contributors if p != pid))

    def _fresh_cid(self) -> int:
        cid = self._next_cid
        self._next_cid += 1
        return cid

    # -------------------------------------------------------------- accessors
    def cell_expr(self, cell) -> RationalFunc | None:
        return _fold_exprs([self.pieces[p].expr for p in cell.contributors])

    def cell_sources(self, cell) -> list[str]:
        return [self.pieces[p].source for p in cell.contributors]

    def cells_around(self, b: Fraction):
        """Return (left segment, point cell, right segment) at boundary ``b``."""
        for i, point in enumerate(self.points):
            if point.value == b:
                left = self.segs[i] if i >= 0 else None
                right = self.segs[i + 1] if i + 1 < len(self.segs) else None
                return left, point, right
        raise KeyError(f"{b} is not a boundary")

    def evaluate(self, x) -> Fraction | None:
        """Exact value at a rational point; None where undefined."""
        x = Fraction(x)
        for point in self.points:
            if point.value == x:
                expr = self.cell_expr(point)
                if expr is None or not expr.is_defined_at(x):
                    return None
                return expr.eval(x)
        for seg in self.segs:
            if (seg.lo is None or seg.lo < x) and (seg.hi is None or x < seg.hi):
                expr = self.cell_expr(seg)
                if expr is None or not expr.is_defined_at(x):
                    return None
                return expr.eval(x)
        return None

    def pieces_out(self) -> list[Piece]:
        return sorted(self.pieces.values(),
                      key=lambda p: (p.interval.lo is not None, p.interval.lo,
                                     p.interval.hi is not None, p.interval.hi))

    # -------------------------------------------------------------- combining
    @staticmethod
    def combine(left: "PiecewiseModel", right: "PiecewiseModel",
                op: str, name: str = "h") -> "PiecewiseModel":
        """Combine two models on the union partition (自动细分交叠区间).

        ``op`` is ``'add'`` or ``'mul'`` and applies across the two models;
        overlapping pieces inside one model always sum.  Cells where either
        side has no contributor stay undefined (空洞保留未定义).
        """
        bounds = sorted(set(left.boundaries) | set(right.boundaries))
        raw: list[tuple[Interval, RationalFunc, str]] = []
        seq: list[tuple[str, object]] = [
            ("seg", (None, bounds[0] if bounds else None))]
        for i, b in enumerate(bounds):
            seq.append(("point", b))
            hi = bounds[i + 1] if i + 1 < len(bounds) else None
            seq.append(("seg", (b, hi)))
        for kind, payload in seq:
            if kind == "point":
                b = payload
                l_expr, l_src = left._expr_at(b)
                r_expr, r_src = right._expr_at(b)
                src = l_src + r_src
                interval = Interval.point(b)
            else:
                lo, hi = payload
                l_cell = left._seg_cell(lo, hi)
                r_cell = right._seg_cell(lo, hi)
                l_expr = left.cell_expr(l_cell) if l_cell else None
                r_expr = right.cell_expr(r_cell) if r_cell else None
                src = left.cell_sources(l_cell) if l_cell else []
                src += right.cell_sources(r_cell) if r_cell else []
                interval = Interval(lo, hi, False, False)
            expr = _combine_exprs(l_expr, r_expr, op)
            if expr is not None:
                if kind == "point" and not expr.is_defined_at(payload):
                    # 空洞保留未定义：该点不生成片段，也不允许跨点合并。
                    continue
                raw.append((interval, expr, "+".join(src)))
        result = PiecewiseModel(name)
        for interval, expr, src in _coalesce(raw):
            result.set_piece(interval, expr, src)
        result.edits.append(
            f"combine({left.name} {op} {right.name})：并集边界 {len(bounds)} 个，"
            f"合并后 {len(result.pieces)} 段"
        )
        return result

    def _point_cell(self, b: Fraction) -> PointCell | None:
        for point in self.points:
            if point.value == b:
                return point
        return None

    def _expr_at(self, b: Fraction) -> tuple[RationalFunc | None, list[str]]:
        """Expression and sources exactly at ``b``.

        ``b`` may be a boundary of the *union* partition but lie strictly
        inside one of this model's segments; in that case the segment's
        expression governs the point value.
        """
        point = self._point_cell(b)
        if point is not None:
            if not point.contributors:
                return None, []
            return self.cell_expr(point), self.cell_sources(point)
        for seg in self.segs:
            if (seg.lo is None or seg.lo < b) and (seg.hi is None or b < seg.hi):
                if not seg.contributors:
                    return None, []
                return self.cell_expr(seg), self.cell_sources(seg)
        return None, []

    def _seg_cell(self, lo, hi) -> SegCell | None:
        for seg in self.segs:
            lo_ok = seg.lo is None if lo is None else (seg.lo is None or seg.lo <= lo)
            hi_ok = seg.hi is None if hi is None else (seg.hi is None or seg.hi >= hi)
            if lo_ok and hi_ok:
                return seg
        return None


def _combine_exprs(l_expr, r_expr, op) -> RationalFunc | None:
    if l_expr is None or r_expr is None:
        return None
    return l_expr + r_expr if op == "add" else l_expr * r_expr


def _coalesce(raw: list[tuple[Interval, RationalFunc, str]]):
    """Merge adjacent pieces with equal expressions across *defined* points.

    Two touching pieces merge only when the shared endpoint belongs to at
    least one of them; a gap (undefined point) therefore always separates
    pieces, even when the expressions are identical.
    """
    out: list[tuple[Interval, RationalFunc, str]] = []
    for interval, expr, src in raw:
        if out:
            last_interval, last_expr, last_src = out[-1]
            touching = last_interval.hi is not None and last_interval.hi == interval.lo
            covered = last_interval.hi_closed or interval.lo_closed
            if touching and covered and last_expr == expr and last_src == src:
                merged = Interval(last_interval.lo, interval.hi,
                                  last_interval.lo_closed, interval.hi_closed)
                out[-1] = (merged, expr, src)
                continue
        out.append((interval, expr, src))
    return out
