"""Priority-stacked interval values resolved onto a non-overlapping timeline.

Semantics
---------
* Source segments are half-open ``[start, end)`` and carry a ``value``, a
  numeric ``priority`` and a ``source`` tag.
* At every point, the covering segments with the highest priority win.
* If the winners all agree on the value, the resolved piece carries that
  value and the *full* set of winning sources (provenance).
* If the winners disagree (same priority, different values), the conflict is
  preserved explicitly as a :class:`Conflict` piece listing every
  ``(value, sources)`` alternative -- nothing is silently dropped.
* Adjacent resolved pieces are merged only when value, conflict status and
  the *entire* source set are identical, so equal-valued pieces from
  different sources never lose provenance evidence.

Updates are local: mutating a segment invalidates only the resolved cache
over that segment's span; the rest of the resolved timeline is reused.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import count
from typing import Dict, Hashable, List, Optional, Tuple

from .timeaxis import Duration, Instant, OffsetTable, WallClock


@dataclass(frozen=True)
class Conflict:
    """Explicit record of a same-priority disagreement."""

    choices: Tuple[Tuple[Hashable, frozenset], ...]
    # choices: ((value, frozenset_of_sources), ...), deterministically ordered

    @property
    def values(self) -> frozenset:
        return frozenset(v for v, _ in self.choices)


@dataclass(frozen=True)
class Piece:
    """One resolved, non-overlapping span of the timeline."""

    start: Instant
    end: Instant
    value: Optional[Hashable]          # None when ``conflict`` is set
    sources: frozenset                 # winning sources over this span
    conflict: Optional[Conflict] = None

    @property
    def duration(self) -> Duration:
        return self.end - self.start

    @property
    def is_conflict(self) -> bool:
        return self.conflict is not None


@dataclass(frozen=True)
class _Segment:
    start: Fraction
    end: Fraction
    value: Hashable
    priority: int
    source: Hashable
    token: int


def _mergeable(a: Piece, b: Piece) -> bool:
    return (
        a.end == b.start
        and a.value == b.value
        and a.sources == b.sources
        and a.conflict == b.conflict
    )


def _merge(a: Piece, b: Piece) -> Piece:
    return Piece(a.start, b.end, a.value, a.sources, a.conflict)


class Timeline:
    """A resolvable stack of priority-tagged interval values."""

    def __init__(self, offset_table: Optional[OffsetTable] = None):
        self._table = offset_table
        self._segments: Dict[int, _Segment] = {}
        self._tokens = count(1)
        # Resolved cache plus the (half-open) dirty window awaiting re-resolve.
        self._resolved: List[Piece] = []
        self._dirty: Optional[Tuple[Fraction, Fraction]] = None

    # ------------------------------------------------------------------ API

    def add(self, source, start, end, value, priority: int = 0) -> int:
        """Insert a source segment; returns a token usable with :meth:`remove`."""
        lo = self._coerce(start)
        hi = self._coerce(end)
        if not hi > lo:
            raise ValueError(f"empty or inverted segment [{lo}, {hi})")
        token = next(self._tokens)
        self._segments[token] = _Segment(lo, hi, value, priority, source, token)
        self._invalidate(lo, hi)
        return token

    def remove(self, token: int) -> None:
        """Withdraw a previously added segment (restores whatever it covered)."""
        seg = self._segments.pop(token)  # KeyError on unknown token, by design
        self._invalidate(seg.start, seg.end)

    def pieces(self, start=None, end=None) -> List[Piece]:
        """Resolved non-overlapping pieces, optionally clipped to [start, end)."""
        self._ensure_fresh()
        lo = self._coerce(start) if start is not None else None
        hi = self._coerce(end) if end is not None else None
        out = []
        for p in self._resolved:
            if lo is not None and p.end.offset <= lo:
                continue
            if hi is not None and p.start.offset >= hi:
                continue
            s = p.start if lo is None or p.start.offset >= lo else Instant(lo)
            e = p.end if hi is None or p.end.offset <= hi else Instant(hi)
            out.append(Piece(s, e, p.value, p.sources, p.conflict))
        return out

    def value_at(self, t):
        """Resolved value at a point: ``(value, sources)``, ``(Conflict, sources)`` or ``None``."""
        self._ensure_fresh()
        x = self._coerce(t)
        for p in self._resolved:
            if p.start.offset <= x < p.end.offset:
                if p.is_conflict:
                    return p.conflict, p.sources
                return p.value, p.sources
        return None

    def segments(self) -> List[_Segment]:
        """Raw (unresolved) source segments, for inspection/debugging."""
        return sorted(self._segments.values(), key=lambda s: (s.start, s.end, s.token))

    # ------------------------------------------------------------- internals

    def _coerce(self, t) -> Fraction:
        if isinstance(t, Instant):
            return t.offset
        if isinstance(t, WallClock):
            if self._table is None:
                raise TypeError("WallClock input requires an offset table")
            return self._table.to_instant(t).offset
        if isinstance(t, Duration):
            raise TypeError("a Duration is a length, not a point in time")
        return Fraction(t)

    def _invalidate(self, lo: Fraction, hi: Fraction) -> None:
        if self._dirty is None:
            self._dirty = (lo, hi)
        else:
            d_lo, d_hi = self._dirty
            self._dirty = (min(d_lo, lo), max(d_hi, hi))

    def _ensure_fresh(self) -> None:
        if self._dirty is None:
            return
        lo, hi = self._dirty
        self._dirty = None
        new = self._resolve_window(lo, hi)
        # Splice: cached pieces outside [lo, hi) stay valid (resolution is
        # pointwise); pieces straddling a window edge are clipped, not dropped.
        kept_left: List[Piece] = []
        kept_right: List[Piece] = []
        for p in self._resolved:
            if p.end.offset <= lo:
                kept_left.append(p)
            elif p.start.offset >= hi:
                kept_right.append(p)
            else:
                if p.start.offset < lo:
                    kept_left.append(Piece(p.start, Instant(lo), p.value, p.sources, p.conflict))
                if p.end.offset > hi:
                    kept_right.append(Piece(Instant(hi), p.end, p.value, p.sources, p.conflict))
        spliced = kept_left + new + kept_right
        # Re-merge across the splice boundaries (never across differing sources).
        merged: List[Piece] = []
        for p in spliced:
            if merged and _mergeable(merged[-1], p):
                merged[-1] = _merge(merged[-1], p)
            else:
                merged.append(p)
        self._resolved = merged

    def _resolve_window(self, lo: Fraction, hi: Fraction) -> List[Piece]:
        covering = [
            s for s in self._segments.values() if s.start < hi and s.end > lo
        ]
        bounds = {lo, hi}
        for s in covering:
            bounds.add(max(s.start, lo))
            bounds.add(min(s.end, hi))
        pts = sorted(bounds)
        out: List[Piece] = []
        for a, b in zip(pts, pts[1:]):
            if a >= b:
                continue
            winners = [s for s in covering if s.start <= a and s.end >= b]
            if not winners:
                continue
            best = max(s.priority for s in winners)
            tops = [s for s in winners if s.priority == best]
            by_value: Dict[Hashable, set] = {}
            for s in tops:
                by_value.setdefault(s.value, set()).add(s.source)
            all_sources = frozenset(s.source for s in tops)
            if len(by_value) == 1:
                (only_value,) = by_value.keys()
                piece = Piece(Instant(a), Instant(b), only_value, all_sources, None)
            else:
                choices = tuple(
                    sorted(
                        ((v, frozenset(srcs)) for v, srcs in by_value.items()),
                        key=lambda item: (repr(item[0]), sorted(map(repr, item[1]))),
                    )
                )
                piece = Piece(Instant(a), Instant(b), None, all_sources, Conflict(choices))
            if out and _mergeable(out[-1], piece):
                out[-1] = _merge(out[-1], piece)
            else:
                out.append(piece)
        return out
