"""Reconstruct half-open occupancy intervals from possibly out-of-order
start/end records, and answer peak-concurrency queries with ID evidence.

Design notes (see README.md for the full discussion):

* Updates never rescan all intervals. Each formed/removed/corrected interval
  only touches a difference map (``_DeltaIndex``) at its two endpoints, plus
  constant-size event maps keyed by endpoint time.
* Queries sweep only the endpoint events that fall inside the query range.
* Orphan (unpaired) records are kept separately and never padded with a fake
  endpoint, so they contribute nothing to the index (no phantom occupancy).
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right, insort
from dataclasses import dataclass
from enum import Enum
from typing import Any, Dict, Hashable, List, Optional, Set, Tuple


class Result(Enum):
    """Explicit outcome of every mutating operation."""

    RECORDED = "recorded"        # orphan endpoint stored, waiting for its counterpart
    PAIRED = "paired"            # both endpoints known; half-open interval indexed
    INVALID_PAIR = "invalid_pair"  # stored but end <= start; not indexed, listed as invalid
    DUPLICATE = "duplicate"      # identical endpoint already known; ignored
    CONFLICT = "conflict"        # different value already known; use correct_* explicitly
    CORRECTED = "corrected"      # endpoint correction applied (index updated atomically)
    INVALID = "invalid"          # correction rejected: it would produce end <= start
    RETRACTED = "retracted"      # occupancy withdrawn and fully removed
    NOT_FOUND = "not_found"      # unknown occupancy id or endpoint


class _DeltaIndex:
    """Difference map over endpoint times.

    ``add``/``remove`` are O(log K) (K = number of distinct endpoint times):
    only the two touched endpoints are updated. ``prefix`` and iteration over
    a range cost O(number of distinct endpoints scanned), never O(intervals).
    """

    def __init__(self) -> None:
        self._delta: Dict[float, int] = {}
        self._times: List[float] = []  # sorted, only times with non-zero delta

    def _bump(self, t: float, d: int) -> None:
        cur = self._delta.get(t)
        if cur is None:
            self._delta[t] = d
            insort(self._times, t)
        else:
            new = cur + d
            if new == 0:
                del self._delta[t]
                self._times.pop(bisect_left(self._times, t))
            else:
                self._delta[t] = new

    def add(self, start: float, end: float) -> None:
        self._bump(start, +1)
        self._bump(end, -1)

    def remove(self, start: float, end: float) -> None:
        self._bump(start, -1)
        self._bump(end, +1)

    def prefix(self, t: float) -> int:
        """Concurrency at instant t (half-open: counts intervals [s, e) with s <= t < e)."""
        return sum(self._delta[x] for x in self._times[: bisect_right(self._times, t)])


@dataclass(frozen=True)
class PeakPeriod:
    """A maximal half-open period at peak concurrency, with its evidence."""

    start: float
    end: float
    occupancy_ids: frozenset  # ids active throughout [start, end)


@dataclass(frozen=True)
class PeakResult:
    peak: int
    periods: Tuple[PeakPeriod, ...]  # all maximal periods reaching `peak`


@dataclass
class _Occupancy:
    start: Optional[float] = None
    end: Optional[float] = None


class OccupancyTracker:
    """Pairs start/end records into half-open intervals and serves peak queries."""

    def __init__(self) -> None:
        self._occ: Dict[Hashable, _Occupancy] = {}
        self._index = _DeltaIndex()
        self._active: Dict[Hashable, Tuple[float, float]] = {}
        self._active_starts: List[Tuple[float, Hashable]] = []  # sorted by start
        self._by_start: Dict[float, Set[Hashable]] = {}
        self._by_end: Dict[float, Set[Hashable]] = {}
        self._event_times: List[float] = []  # sorted union of by_start/by_end keys

    # ------------------------------------------------------------------ index

    def _index_add(self, oid: Hashable, start: float, end: float) -> None:
        self._index.add(start, end)
        self._active[oid] = (start, end)
        insort(self._active_starts, (start, oid), key=lambda p: p[0])
        for t, m in ((start, self._by_start), (end, self._by_end)):
            if t not in self._by_start and t not in self._by_end:
                insort(self._event_times, t)
            m.setdefault(t, set()).add(oid)

    def _index_remove(self, oid: Hashable) -> None:
        start, end = self._active.pop(oid)
        self._index.remove(start, end)
        i = bisect_left(self._active_starts, start, key=lambda p: p[0])
        while self._active_starts[i][1] != oid:
            i += 1
        self._active_starts.pop(i)
        for t, m in ((start, self._by_start), (end, self._by_end)):
            ids = m[t]
            ids.discard(oid)
            if not ids:
                del m[t]
            if t not in self._by_start and t not in self._by_end:
                self._event_times.pop(bisect_left(self._event_times, t))

    # ---------------------------------------------------------------- records

    def record_start(self, oid: Hashable, t: float) -> Result:
        occ = self._occ.setdefault(oid, _Occupancy())
        if occ.start is not None:
            return Result.DUPLICATE if occ.start == t else Result.CONFLICT
        occ.start = t
        return self._try_pair(oid, occ)

    def record_end(self, oid: Hashable, t: float) -> Result:
        occ = self._occ.setdefault(oid, _Occupancy())
        if occ.end is not None:
            return Result.DUPLICATE if occ.end == t else Result.CONFLICT
        occ.end = t
        return self._try_pair(oid, occ)

    def _try_pair(self, oid: Hashable, occ: _Occupancy) -> Result:
        if occ.start is None or occ.end is None:
            return Result.RECORDED
        if occ.end <= occ.start:
            # Stored as an invalid pair: never indexed, listed via orphans().
            return Result.INVALID_PAIR
        self._index_add(oid, occ.start, occ.end)
        return Result.PAIRED

    # ------------------------------------------------------------- corrections

    def correct_start(self, oid: Hashable, t: float) -> Result:
        occ = self._occ.get(oid)
        if occ is None or occ.start is None:
            return Result.NOT_FOUND
        if occ.end is not None and occ.end <= t:
            return Result.INVALID  # rejected: prior state untouched
        if oid in self._active:
            self._index_remove(oid)
        occ.start = t
        if occ.end is not None:
            self._index_add(oid, occ.start, occ.end)
        return Result.CORRECTED

    def correct_end(self, oid: Hashable, t: float) -> Result:
        occ = self._occ.get(oid)
        if occ is None or occ.end is None:
            return Result.NOT_FOUND
        if occ.start is not None and t <= occ.start:
            return Result.INVALID  # rejected: prior state untouched
        if oid in self._active:
            self._index_remove(oid)
        occ.end = t
        if occ.start is not None:
            self._index_add(oid, occ.start, occ.end)
        return Result.CORRECTED

    # --------------------------------------------------------------- retraction

    def retract(self, oid: Hashable) -> Result:
        occ = self._occ.pop(oid, None)
        if occ is None:
            return Result.NOT_FOUND
        if oid in self._active:
            self._index_remove(oid)
        return Result.RETRACTED

    # ------------------------------------------------------------------- views

    def intervals(self) -> Dict[Hashable, Tuple[float, float]]:
        """Currently paired (active) half-open intervals."""
        return dict(self._active)

    def orphans(self) -> Dict[Hashable, Dict[str, Any]]:
        """Unpaired / invalid records, listed separately; never indexed."""
        out: Dict[Hashable, Dict[str, Any]] = {}
        for oid, occ in self._occ.items():
            if oid in self._active:
                continue
            if occ.start is not None and occ.end is not None:
                status = "invalid"
            elif occ.start is not None:
                status = "waiting_end"
            else:
                status = "waiting_start"
            out[oid] = {"start": occ.start, "end": occ.end, "status": status}
        return out

    def count_at(self, t: float) -> int:
        return self._index.prefix(t)

    # ------------------------------------------------------------------- query

    def peak(self, lo: Optional[float] = None, hi: Optional[float] = None) -> PeakResult:
        """Peak concurrency in [lo, hi) and every maximal period reaching it.

        Sweeps only the endpoint events inside the range; the initial active
        set at ``lo`` is resolved by a stabbing scan over intervals starting
        at or before ``lo`` (see README for scaling notes).
        """
        lo = float("-inf") if lo is None else float(lo)
        hi = float("inf") if hi is None else float(hi)
        if not hi > lo:
            raise ValueError("empty query range")

        i = bisect_right(self._active_starts, lo, key=lambda p: p[0])
        active: Set[Hashable] = {
            oid for _, oid in self._active_starts[:i] if self._active[oid][1] > lo
        }

        segments: List[Tuple[float, float, frozenset]] = []
        seg_start = lo
        j = bisect_right(self._event_times, lo)
        while j < len(self._event_times):
            t = self._event_times[j]
            if t >= hi:
                break
            segments.append((seg_start, t, frozenset(active)))
            for oid in self._by_end.get(t, ()):  # ends before starts: half-open
                active.discard(oid)
            for oid in self._by_start.get(t, ()):
                active.add(oid)
            seg_start = t
            j += 1
        segments.append((seg_start, hi, frozenset(active)))

        best = 0
        periods: List[PeakPeriod] = []
        for s, e, ids in segments:
            c = len(ids)
            if c == 0 or s == e:
                continue
            if c > best:
                best = c
                periods = []
            if c == best:
                if periods and periods[-1].end == s and periods[-1].occupancy_ids == ids:
                    periods[-1] = PeakPeriod(periods[-1].start, e, ids)
                else:
                    periods.append(PeakPeriod(s, e, ids))
        return PeakResult(best, tuple(periods))
