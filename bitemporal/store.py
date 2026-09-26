"""Bitemporal store.

Every record carries:

* a **valid-time** interval ``[valid_from, valid_to)`` (business time;
  ``valid_to=None`` means open ended), and
* a **recorded-version** span ``[version_from, version_to)`` (observation
  version; ``version_to=None`` means still current).

Each transaction (upsert / retract / priority change, possibly batched) is
applied atomically under a new, strictly increasing version.  A retroactive
correction closes the rectangles it overlaps at the new version and opens
new ones -- one correction may split many rectangles -- but it never mutates
rectangles already closed, so earlier observation versions are immutable.
Retraction closes rectangles instead of deleting them: history is never
overwritten.

At any (version, valid-time) point the *visible* record is the covering
record with the highest explicit ``priority``; ties break deterministically
on the larger ``record_id``.  The store materialises the visible set as
disjoint canonical fragments in a :class:`RectangleIndex`.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from typing import Any, Optional

from .rectindex import RectangleIndex
from .timekit import TimePoint

# ---------------------------------------------------------------------------
# operations


@dataclass(frozen=True)
class Upsert:
    record_id: str
    valid_from: TimePoint
    valid_to: Optional[TimePoint]  # None = open ended
    value: Any
    priority: int = 0


@dataclass(frozen=True)
class Retract:
    record_id: str


@dataclass(frozen=True)
class ChangePriority:
    record_id: str
    priority: int


# ---------------------------------------------------------------------------
# result views


@dataclass(frozen=True)
class FragmentView:
    """One canonical visible fragment (disjoint from all others)."""

    record_id: str
    value: Any
    priority: int
    valid_from: Optional[TimePoint]
    valid_to: Optional[TimePoint]
    version_from: int
    version_to: Optional[int]


@dataclass(frozen=True)
class Contender:
    """A record covering the explained point, with its disposition."""

    record_id: str
    value: Any
    priority: int
    version_from: int
    valid_from: TimePoint
    valid_to: Optional[TimePoint]
    visible: bool
    reason: str


@dataclass(frozen=True)
class Explanation:
    """Provenance for a point query: the winner plus every shadowed record."""

    version: int
    point: TimePoint
    fragment: Optional[FragmentView]
    contenders: tuple[Contender, ...]


@dataclass(frozen=True)
class RawRectangle:
    """Versioned state of one record (its raw bitemporal rectangle)."""

    version_from: int
    version_to: Optional[int]
    valid_from: TimePoint
    valid_to: Optional[TimePoint]
    value: Any
    priority: int


@dataclass(frozen=True)
class _RecordState:
    valid_from: TimePoint
    valid_to: Optional[TimePoint]
    value: Any
    priority: int


@dataclass(frozen=True)
class _VisiblePayload:
    record_id: str
    value: Any
    priority: int


def _overlaps(lo1, hi1, lo2, hi2) -> bool:
    """Half-open interval overlap; None low = -infinity, None high = +infinity."""
    if hi1 is not None and lo2 is not None and lo2 >= hi1:
        return False
    if hi2 is not None and lo1 is not None and lo1 >= hi2:
        return False
    return True


class BitemporalStore:
    def __init__(self):
        self._version = 0
        self._records: dict[str, _RecordState] = {}
        # visible fragments: payload = _VisiblePayload | None
        self._visible = RectangleIndex(empty_payload=None)
        # raw record presence: payload = tuple of record_ids covering the segment
        self._raw = RectangleIndex(empty_payload=())
        # per-record append-only history of raw rectangles
        self._history: dict[str, list[RawRectangle]] = {}
        self._history_vfroms: dict[str, list[int]] = {}
        self._op_log: list[tuple[int, tuple]] = []

    # -- introspection -------------------------------------------------------

    @property
    def version(self) -> int:
        return self._version

    def index_stats(self) -> dict:
        return {
            "segments": self._visible.segment_count(),
            "rectangles": self._visible.rectangle_count(),
        }

    def reset_probes(self) -> None:
        self._visible.reset_probes()
        self._raw.reset_probes()

    @property
    def probes(self) -> int:
        return self._visible.probes + self._raw.probes

    def op_log(self) -> list[tuple[int, tuple]]:
        return list(self._op_log)

    # -- transactions ----------------------------------------------------------

    def transact(self, ops) -> int:
        """Apply a batch of operations atomically as one new version.

        All validation happens before any mutation; if any operation is
        invalid the store is left completely untouched.
        """
        ops = tuple(ops)
        if not ops:
            raise ValueError("empty transaction")

        new_records = dict(self._records)
        changed: dict[str, list] = {}  # rid -> [old_state, new_state]
        touched: list[tuple[TimePoint, Optional[TimePoint]]] = []

        def note(rid, old, new):
            if old == new:
                return
            if rid not in changed:
                changed[rid] = [old, new]
            else:
                changed[rid][1] = new
            if old is not None:
                touched.append((old.valid_from, old.valid_to))
            if new is not None:
                touched.append((new.valid_from, new.valid_to))

        for op in ops:
            if isinstance(op, Upsert):
                if op.valid_to is not None and not (op.valid_from < op.valid_to):
                    raise ValueError(
                        f"invalid interval for {op.record_id!r}: "
                        f"{op.valid_from!r} .. {op.valid_to!r}"
                    )
                old = new_records.get(op.record_id)
                new = _RecordState(op.valid_from, op.valid_to, op.value, op.priority)
                new_records[op.record_id] = new
                note(op.record_id, old, new)
            elif isinstance(op, Retract):
                old = new_records.get(op.record_id)
                if old is None:
                    raise KeyError(f"cannot retract unknown record {op.record_id!r}")
                del new_records[op.record_id]
                note(op.record_id, old, None)
            elif isinstance(op, ChangePriority):
                old = new_records.get(op.record_id)
                if old is None:
                    raise KeyError(
                        f"cannot change priority of unknown record {op.record_id!r}"
                    )
                new = _RecordState(old.valid_from, old.valid_to, old.value, op.priority)
                new_records[op.record_id] = new
                note(op.record_id, old, new)
            else:
                raise TypeError(f"unknown operation {op!r}")

        version = self._version + 1
        if touched:
            self._recompute(new_records, touched, version)
        for rid, (old, new) in changed.items():
            self._rewrite_history(rid, old, new, version)
        self._records = new_records
        self._version = version
        self._op_log.append((version, ops))
        return version

    def upsert(self, record_id, valid_from, valid_to, value, priority=0) -> int:
        return self.transact([Upsert(record_id, valid_from, valid_to, value, priority)])

    def retract(self, record_id) -> int:
        return self.transact([Retract(record_id)])

    def change_priority(self, record_id, priority) -> int:
        return self.transact([ChangePriority(record_id, priority)])

    # -- the rectangle-splitting core ------------------------------------------

    def _recompute(self, new_records, touched, version) -> None:
        vis, raw = self._visible, self._raw

        # Cut points: touched endpoints plus every endpoint of a current
        # record intersecting the touched region, so each segment inside the
        # region is elementary w.r.t. the new record set.
        cuts: set[TimePoint] = set()
        for lo, hi in touched:
            cuts.add(lo)
            if hi is not None:
                cuts.add(hi)
        for rec in new_records.values():
            if any(_overlaps(lo, hi, rec.valid_from, rec.valid_to) for lo, hi in touched):
                cuts.add(rec.valid_from)
                if rec.valid_to is not None:
                    cuts.add(rec.valid_to)
        for cut in sorted(cuts):
            vis.split_at(cut)
            raw.split_at(cut)

        # Recompute every segment inside the touched region.
        affected: set[int] = set()
        segments = vis._segments
        for lo, hi in touched:
            i = vis._locate(lo)
            while i < len(segments):
                seg = segments[i]
                if hi is not None and seg.lo is not None and seg.lo >= hi:
                    break
                affected.add(i)
                i += 1

        for i in sorted(affected):
            seg = segments[i]
            covering = [
                (rid, rec)
                for rid, rec in new_records.items()
                if _overlaps(seg.lo, seg.hi, rec.valid_from, rec.valid_to)
            ]
            rids = tuple(sorted(rid for rid, _ in covering))
            if covering:
                rid, rec = max(covering, key=lambda item: (item[1].priority, item[0]))
                payload = _VisiblePayload(rid, rec.value, rec.priority)
            else:
                payload = None
            vis.set_payload(i, version, payload)
            raw.set_payload(i, version, rids)

        # Canonicalise: merge adjacent segments whose whole history is
        # identical in *both* indices (keeps both indices in lockstep).
        if affected:
            i = max(0, min(affected) - 1)
            last = min(max(affected) + 1, len(vis._segments) - 1)
            while i < min(last, len(vis._segments) - 1):
                if vis._segments[i].entries == vis._segments[i + 1].entries and (
                    raw._segments[i].entries == raw._segments[i + 1].entries
                ):
                    vis.try_merge(i)
                    raw.try_merge(i)
                else:
                    i += 1

    def _rewrite_history(self, rid, old, new, version) -> None:
        rects = self._history.setdefault(rid, [])
        vfroms = self._history_vfroms.setdefault(rid, [])
        if old is not None:
            last = rects[-1]
            assert last.version_to is None
            rects[-1] = RawRectangle(
                last.version_from, version, last.valid_from, last.valid_to,
                last.value, last.priority,
            )
        if new is not None:
            rects.append(RawRectangle(
                version, None, new.valid_from, new.valid_to, new.value, new.priority,
            ))
            vfroms.append(version)

    # -- queries ------------------------------------------------------------

    def _check_version(self, version: int) -> None:
        if not 0 <= version <= self._version:
            raise ValueError(
                f"version {version} outside observable range 0..{self._version}"
            )

    def value_at(self, version: int, point: TimePoint) -> Optional[FragmentView]:
        """The visible value at a business-time point, as of an observation
        version.  Never scans history: two bisections in the index."""
        self._check_version(version)
        entry, lo, hi = self._visible.point_query(version, point)
        if entry.payload is None:
            return None
        p = entry.payload
        return FragmentView(p.record_id, p.value, p.priority, lo, hi,
                            entry.version_from, entry.version_to)

    def scan(
        self,
        version_from: int = 0,
        version_to: Optional[int] = None,
        valid_from: Optional[TimePoint] = None,
        valid_to: Optional[TimePoint] = None,
    ) -> list[FragmentView]:
        """All visible fragments intersecting the version x valid-time box."""
        self._check_version(version_from)
        if version_to is not None:
            self._check_version(version_to - 1)
        out = []
        for rect in self._visible.range_query(version_from, version_to,
                                              valid_from, valid_to):
            if rect.payload is None:
                continue
            p = rect.payload
            out.append(FragmentView(p.record_id, p.value, p.priority,
                                    rect.valid_from, rect.valid_to,
                                    rect.version_from, rect.version_to))
        return out

    def explain(self, version: int, point: TimePoint) -> Explanation:
        """Provenance: which record supplies the value here, and which
        records were shadowed (with the reason)."""
        self._check_version(version)
        entry, lo, hi = self._visible.point_query(version, point)
        raw_entry, _, _ = self._raw.point_query(version, point)
        winner_id = entry.payload.record_id if entry.payload else None
        contenders = []
        for rid in raw_entry.payload:
            rect = self._rect_at(rid, version)
            visible = rid == winner_id
            if visible:
                reason = "visible: highest priority"
            else:
                w = entry.payload
                if rect.priority < w.priority:
                    reason = f"shadowed: priority {rect.priority} < {w.priority}"
                else:
                    reason = (
                        f"shadowed: equal priority {rect.priority}, "
                        f"tie broken on record_id {w.record_id!r}"
                    )
            contenders.append(Contender(
                rid, rect.value, rect.priority, rect.version_from,
                rect.valid_from, rect.valid_to, visible, reason,
            ))
        contenders.sort(key=lambda c: (c.priority, c.record_id), reverse=True)
        fragment = None
        if entry.payload is not None:
            p = entry.payload
            fragment = FragmentView(p.record_id, p.value, p.priority, lo, hi,
                                    entry.version_from, entry.version_to)
        return Explanation(version, point, fragment, tuple(contenders))

    def history_of(self, record_id: str) -> list[RawRectangle]:
        """Every raw rectangle of a record, oldest first (retractions keep
        history: closed rectangles are never removed)."""
        return list(self._history.get(record_id, []))

    def _rect_at(self, record_id: str, version: int) -> RawRectangle:
        rects = self._history[record_id]
        vfroms = self._history_vfroms[record_id]
        j = bisect_right(vfroms, version) - 1
        rect = rects[j]
        assert rect.version_from <= version
        assert rect.version_to is None or version < rect.version_to
        return rect
