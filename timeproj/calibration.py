"""Synthetic offset conversion tables and versioned calibration.

A calibration table maps *raw* wall-clock readings to corrected absolute
times by adding a piecewise-linear, exactly-represented offset.  Tables are
versioned.  The projector caches per-block projection slices and, after a
local calibration correction, recomputes only the slices whose coverage
intersects the corrected span.  Cursors opened earlier keep the historical
version of the table.
"""
from __future__ import annotations

from fractions import Fraction

from .projection import _block_coverage, _block_runs, assemble, project
from .types import Interval, TimePoint, WallClockTime


def _frac(v) -> Fraction:
    return v if isinstance(v, Fraction) else Fraction(v)


class OffsetTable:
    """Piecewise-linear exact offset table: corrected = raw + offset(raw)."""

    def __init__(self, points, version: int = 0):
        pts = sorted((_frac(r), _frac(o)) for r, o in points)
        if not pts:
            raise ValueError("offset table needs at least one point")
        for (r0, o0), (r1, o1) in zip(pts, pts[1:]):
            if r1 <= r0:
                raise ValueError("raw readings must be strictly increasing")
            if r1 + o1 <= r0 + o0:
                raise ValueError("corrected time must be strictly increasing")
        self.points = tuple(pts)
        self.version = version

    def offset_at(self, raw) -> Fraction:
        raw = _frac(raw)
        pts = self.points
        if raw <= pts[0][0]:
            return pts[0][1]
        if raw >= pts[-1][0]:
            return pts[-1][1]
        for (r0, o0), (r1, o1) in zip(pts, pts[1:]):
            if r0 <= raw <= r1:
                t = (raw - r0) / (r1 - r0)
                return o0 + t * (o1 - o0)
        raise AssertionError("unreachable")

    def correct(self, raw) -> Fraction:
        raw = _frac(raw)
        return raw + self.offset_at(raw)

    def uncorrect(self, corrected) -> Fraction:
        """Inverse of :meth:`correct` (exact, piecewise linear)."""
        y = _frac(corrected)
        nodes = [(r, r + o) for r, o in self.points]
        if y <= nodes[0][1]:
            return nodes[0][0] + (y - nodes[0][1])
        if y >= nodes[-1][1]:
            return nodes[-1][0] + (y - nodes[-1][1])
        for (r0, c0), (r1, c1) in zip(nodes, nodes[1:]):
            if c0 <= y <= c1:
                t = (y - c0) / (c1 - c0)
                return r0 + t * (r1 - r0)
        raise AssertionError("unreachable")


class CalibrationStore:
    """Keeps every version of the offset table plus the change history."""

    def __init__(self, table: OffsetTable):
        self._tables = {table.version: table}
        self._current = table.version
        self._changes = []  # (new_version, raw_lo, raw_hi)

    @property
    def current_version(self) -> int:
        return self._current

    def table(self, version: int | None = None) -> OffsetTable:
        return self._tables[self._current if version is None else version]

    def update(self, table: OffsetTable, raw_span) -> None:
        """Install a new table version.

        ``raw_span`` records the raw interval the edit touched (audit
        metadata); slice invalidation is derived exactly from the movement
        of the corrected query endpoints.
        """
        if table.version in self._tables:
            raise ValueError(f"version {table.version} already exists")
        lo, hi = _frac(raw_span[0]), _frac(raw_span[1])
        if hi < lo:
            raise ValueError("empty change span")
        self._tables[table.version] = table
        self._changes.append((table.version, lo, hi))
        self._current = table.version

    def changes_between(self, old_version: int, new_version: int) -> list:
        return [(lo, hi) for v, lo, hi in self._changes
                if old_version < v <= new_version]


def _intersects_span(iv: Interval, span) -> bool:
    lo, hi = span
    if iv.end.value < lo or (iv.end.value == lo and iv.end_open):
        return False
    if hi < iv.start.value or (hi == iv.start.value and iv.start_open):
        return False
    return True


class _BlockEntry:
    __slots__ = ("runs", "missing", "evidence", "coverage")

    def __init__(self, runs, missing, evidence, coverage):
        self.runs = runs
        self.missing = missing
        self.evidence = evidence
        self.coverage = coverage


class _QueryState:
    __slots__ = ("version", "entries")

    def __init__(self, version, entries):
        self.version = version
        self.entries = entries


class CalibratedProjector:
    """Projects raw (wall-clock) queries through the current calibration.

    Per-block results are cached per raw query.  When the calibration is
    corrected locally, only blocks whose coverage intersects the corrected
    span are recomputed; every other block reuses its cached slices.
    """

    def __init__(self, store: CalibrationStore, blocks):
        self.store = store
        self.blocks = {(b.epoch, b.version): b for b in blocks}
        self._states = {}  # raw Interval -> _QueryState

    def _corrected(self, raw_query: Interval, table: OffsetTable) -> Interval:
        return Interval(
            TimePoint(table.correct(raw_query.start.value)),
            TimePoint(table.correct(raw_query.end.value)),
            raw_query.start_open, raw_query.end_open)

    def open_cursor(self, version: int | None = None) -> "Cursor":
        if version is None:
            version = self.store.current_version
        return Cursor(self, version)

    def project(self, raw_query: Interval):
        version = self.store.current_version
        table = self.store.table()
        cquery = self._corrected(raw_query, table)
        state = self._states.get(raw_query)
        spans = []
        if state is not None and state.version != version:
            # The projection depends on the calibration only through the
            # corrected query endpoints, so the exact invalidation region
            # is wherever those endpoints actually moved.
            old_cquery = self._corrected(raw_query,
                                         self.store.table(state.version))
            for old_v, new_v in ((old_cquery.start.value, cquery.start.value),
                                 (old_cquery.end.value, cquery.end.value)):
                if old_v != new_v:
                    spans.append((min(old_v, new_v), max(old_v, new_v)))
        if state is None or state.version != version:
            previous = state.entries if state is not None else {}
            entries = {}
            for key, block in self.blocks.items():
                entry = previous.get(key)
                if entry is not None:
                    region = entry.coverage
                    if region is None:
                        region = _block_coverage(block, cquery)
                    if region is None or not any(
                            _intersects_span(region, s) for s in spans):
                        entries[key] = entry  # reuse untouched slices
                        continue
                runs, missing, evidence, cov = _block_runs(cquery, block)
                entries[key] = _BlockEntry(runs, missing, evidence, cov)
            state = _QueryState(version, entries)
            self._states[raw_query] = state
        runs = {k: [list(r) for r in e.runs] for k, e in state.entries.items()}
        missing = [m for e in state.entries.values() for m in e.missing]
        evidence = [x for e in state.entries.values() for x in e.evidence]
        coverages = [e.coverage for e in state.entries.values()
                     if e.coverage is not None]
        return assemble(cquery, self.blocks, runs, missing, evidence, coverages)

    def project_raw(self, start: WallClockTime, end: WallClockTime,
                    start_open: bool = False, end_open: bool = False):
        """Project a query expressed as wall-clock readings."""
        if start.clock != end.clock:
            raise TypeError("query endpoints are on different clocks")
        return self.project(Interval(TimePoint(start.value),
                                     TimePoint(end.value),
                                     start_open, end_open))


class Cursor:
    """A read handle pinned to the calibration version at open time.

    Calibration updates never affect an existing cursor: it keeps reading
    the historical version of the table.
    """

    def __init__(self, projector: CalibratedProjector, version: int):
        self._projector = projector
        self.version = version

    def project(self, raw_query: Interval):
        table = self._projector.store.table(self.version)
        cquery = self._projector._corrected(raw_query, table)
        return project(cquery, self._projector.blocks.values())
