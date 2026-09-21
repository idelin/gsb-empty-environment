"""Cross-source nearest-sample alignment (incremental as-of join) with corrections.

Model
-----
* The MAIN stream produces *anchors*.  Each anchor is an independent alignment
  target at time ``t``.
* Each auxiliary stream produces *samples*.  For every anchor, each aux stream
  contributes at most one sample: the nearest one with ``|dt| <= tolerance``.
  Ties are broken by earlier ``time``, then by smaller ``seq``.
* Results are versioned per anchor.  A late, better sample (or a retraction
  that changes the best candidate) produces a *replacement* ``AlignedResult``
  with a bumped version, as long as the anchor is not yet finalized.
* Watermarks finalize results: once the main watermark passes the anchor time
  and every aux watermark passes ``anchor_time + tolerance``, the result is
  immutable and late data/retractions for it are ignored.
* Garbage collection never drops a sample that is still within tolerance of an
  unclosed (live) anchor, because a retraction of the current best may need it
  as a fallback candidate.
"""
from __future__ import annotations

from bisect import bisect_left, insort
from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Tuple

MAIN = "__main__"
NEG_INF = float("-inf")


# --------------------------------------------------------------------------
# Input events
# --------------------------------------------------------------------------
@dataclass(frozen=True)
class Anchor:
    anchor_id: str
    time: float
    seq: int


@dataclass(frozen=True)
class Sample:
    source: str
    sample_id: str
    time: float
    seq: int


@dataclass(frozen=True)
class UpsertMain:
    anchor: Anchor


@dataclass(frozen=True)
class RetractMain:
    anchor_id: str


@dataclass(frozen=True)
class UpsertAux:
    sample: Sample


@dataclass(frozen=True)
class RetractAux:
    source: str
    sample_id: str


@dataclass(frozen=True)
class Watermark:
    """Declares that no further events with ``time <= watermark`` will arrive
    for ``source`` (use ``MAIN`` for the anchor stream)."""
    source: str
    time: float


# --------------------------------------------------------------------------
# Output events
# --------------------------------------------------------------------------
@dataclass(frozen=True)
class Match:
    source: str
    sample_id: Optional[str]
    time: Optional[float]
    seq: Optional[int]
    missing: bool


@dataclass(frozen=True)
class AlignedResult:
    """Upsert of one alignment row (a correction when version > 1)."""
    anchor_id: str
    anchor_time: float
    version: int
    matches: Tuple[Match, ...]


@dataclass(frozen=True)
class RetractResult:
    """The anchor was withdrawn; drop the row."""
    anchor_id: str
    version: int


@dataclass(frozen=True)
class FinalizeResult:
    """The row is now immutable (watermark finalization)."""
    anchor_id: str
    version: int


OutputEvent = object  # AlignedResult | RetractResult | FinalizeResult


# --------------------------------------------------------------------------
# Sorted index over (time, seq, item_id)
# --------------------------------------------------------------------------
class _Index:
    __slots__ = ("keys", "items")

    def __init__(self) -> None:
        self.keys: List[Tuple[float, int, str]] = []
        self.items: Dict[str, object] = {}

    def __len__(self) -> int:
        return len(self.keys)

    def add(self, item_id: str, payload) -> None:
        self.remove(item_id)
        insort(self.keys, (payload.time, payload.seq, item_id))
        self.items[item_id] = payload

    def remove(self, item_id: str):
        old = self.items.pop(item_id, None)
        if old is not None:
            i = bisect_left(self.keys, (old.time, old.seq, item_id))
            self.keys.pop(i)
        return old

    def best_match(self, t: float, tol: float):
        """Return the (time, seq, item_id) key of the nearest sample within
        tolerance, tie-broken by earlier time then smaller seq, or None."""
        keys = self.keys
        i = bisect_left(keys, (t,))  # first key with time >= t
        cands = []
        if i < len(keys) and keys[i][0] - t <= tol:
            cands.append(keys[i])
        if i > 0 and t - keys[i - 1][0] <= tol:
            cands.append(keys[i - 1])
        if not cands:
            return None
        best = min(cands, key=lambda k: (abs(k[0] - t), k[0], k[1]))
        # Among samples at the best time, the smallest seq wins.
        j = bisect_left(keys, (best[0],))
        return keys[j]


# --------------------------------------------------------------------------
# Aligner
# --------------------------------------------------------------------------
class Aligner:
    def __init__(self, aux_sources: Iterable[str], tolerance: float) -> None:
        self._sources: Tuple[str, ...] = tuple(aux_sources)
        if MAIN in self._sources:
            raise ValueError(f"{MAIN!r} is reserved")
        self._tol = float(tolerance)
        self._anchors = _Index()
        self._aux = {s: _Index() for s in self._sources}
        self._wm = {MAIN: NEG_INF, **{s: NEG_INF for s in self._sources}}
        self._current: Dict[str, AlignedResult] = {}  # live rows
        self._finalized: set[str] = set()

    # -- queries ----------------------------------------------------------
    def stats(self) -> dict:
        return {
            "live_anchors": len(self._anchors),
            "finalized_anchors": len(self._finalized),
            "samples": {s: len(idx) for s, idx in self._aux.items()},
            "watermarks": dict(self._wm),
        }

    def _matches_for(self, anchor: Anchor) -> Tuple[Match, ...]:
        out = []
        for s in self._sources:
            key = self._aux[s].best_match(anchor.time, self._tol)
            if key is None:
                out.append(Match(s, None, None, None, True))
            else:
                smp = self._aux[s].items[key[2]]
                out.append(Match(s, smp.sample_id, smp.time, smp.seq, False))
        return tuple(out)

    def _anchors_between(self, lo: float, hi: float) -> List[Anchor]:
        keys = self._anchors.keys
        i = bisect_left(keys, (lo,))
        out = []
        while i < len(keys) and keys[i][0] <= hi:
            out.append(self._anchors.items[keys[i][2]])
            i += 1
        return out

    # -- emission ---------------------------------------------------------
    def _emit_upsert(self, anchor: Anchor) -> Optional[AlignedResult]:
        matches = self._matches_for(anchor)
        cur = self._current.get(anchor.anchor_id)
        if cur is not None and cur.matches == matches:
            return None
        version = 1 if cur is None else cur.version + 1
        row = AlignedResult(anchor.anchor_id, anchor.time, version, matches)
        self._current[anchor.anchor_id] = row
        return row

    # -- public API: incremental + batch ----------------------------------
    def apply(self, event) -> List[OutputEvent]:
        if isinstance(event, UpsertMain):
            return self._on_upsert_main(event.anchor)
        if isinstance(event, RetractMain):
            return self._on_retract_main(event.anchor_id)
        if isinstance(event, UpsertAux):
            return self._on_upsert_aux(event.sample)
        if isinstance(event, RetractAux):
            return self._on_retract_aux(event.source, event.sample_id)
        if isinstance(event, Watermark):
            return self._on_watermark(event.source, event.time)
        raise TypeError(f"unknown event: {event!r}")

    def apply_all(self, events: Iterable) -> List[OutputEvent]:
        """Batch interface: apply events in order, return all output events."""
        out: List[OutputEvent] = []
        for ev in events:
            out.extend(self.apply(ev))
        return out

    # -- handlers -----------------------------------------------------------
    def _on_upsert_main(self, anchor: Anchor) -> List[OutputEvent]:
        if anchor.anchor_id in self._finalized:
            return []  # finalized results cannot be rewritten
        self._anchors.add(anchor.anchor_id, anchor)
        row = self._emit_upsert(anchor)
        return [row] if row else []

    def _on_retract_main(self, anchor_id: str) -> List[OutputEvent]:
        if anchor_id in self._finalized:
            return []  # too late: the result is locked
        cur = self._current.pop(anchor_id, None)
        self._anchors.remove(anchor_id)
        if cur is None:
            return []
        return [RetractResult(anchor_id, cur.version)]

    def _on_upsert_aux(self, sample: Sample) -> List[OutputEvent]:
        idx = self._aux.get(sample.source)
        if idx is None:
            raise ValueError(f"unknown aux source: {sample.source!r}")
        idx.add(sample.sample_id, sample)
        return self._recompute_window(sample.time)

    def _on_retract_aux(self, source: str, sample_id: str) -> List[OutputEvent]:
        idx = self._aux.get(source)
        if idx is None:
            raise ValueError(f"unknown aux source: {source!r}")
        old = idx.remove(sample_id)
        if old is None:
            return []
        return self._recompute_window(old.time)

    def _recompute_window(self, t: float) -> List[OutputEvent]:
        out = []
        for anchor in self._anchors_between(t - self._tol, t + self._tol):
            row = self._emit_upsert(anchor)
            if row is not None:
                out.append(row)
        return out

    def _on_watermark(self, source: str, time: float) -> List[OutputEvent]:
        if source != MAIN and source not in self._aux:
            raise ValueError(f"unknown source: {source!r}")
        if time < self._wm[source]:
            raise ValueError(f"watermark regression for {source!r}")
        self._wm[source] = time
        out = self._finalize_due()
        self.gc()
        return out

    def _finalize_due(self) -> List[OutputEvent]:
        # Anchor at t is final once no retraction can hit it (main wm >= t)
        # and no candidate within tolerance can still appear (aux wm >= t+tol).
        horizon = min(
            [self._wm[MAIN]] + [self._wm[s] - self._tol for s in self._sources]
        )
        out = []
        keys = self._anchors.keys
        # Finalizable anchors form a prefix of the time-sorted keys; removing
        # keys[0] shifts the next candidate into position 0.
        while keys and keys[0][0] <= horizon:
            aid = keys[0][2]
            cur = self._current.pop(aid)
            self._anchors.remove(aid)
            self._finalized.add(aid)
            out.append(FinalizeResult(aid, cur.version))
        return out

    # -- resource reclamation ----------------------------------------------
    def gc(self) -> List[Sample]:
        """Drop aux samples no future or live anchor can still match.

        A sample at ``s`` is kept while any unclosed anchor sits within
        ``[s-tol, s+tol]`` (it may become the fallback after a retraction),
        or while future anchors with time ``<= s+tol`` may still arrive.
        """
        removed: List[Sample] = []
        main_wm = self._wm[MAIN]
        akeys = self._anchors.keys
        for s in self._sources:
            idx = self._aux[s]
            for sid, smp in list(idx.items.items()):
                if smp.time + self._tol > main_wm:
                    continue  # future anchors may still need it
                i = bisect_left(akeys, (smp.time - self._tol,))
                if i < len(akeys) and akeys[i][0] <= smp.time + self._tol:
                    continue  # a live (unclosed) anchor may still need it
                idx.remove(sid)
                removed.append(smp)
        return removed


# --------------------------------------------------------------------------
# Consumer: rebuilds the current alignment table from the output event stream
# --------------------------------------------------------------------------
class AlignmentTable:
    """Applies Aligner output events to maintain the current alignment.

    Corrections (``AlignedResult`` with a higher version) simply overwrite the
    row keyed by anchor id, so a consumer that applies the event log in order
    always converges to the current table.
    """

    def __init__(self) -> None:
        self.rows: Dict[str, AlignedResult] = {}
        self.finalized: set[str] = set()

    def apply(self, event: OutputEvent) -> None:
        if isinstance(event, AlignedResult):
            if event.anchor_id not in self.finalized:
                self.rows[event.anchor_id] = event
        elif isinstance(event, RetractResult):
            self.rows.pop(event.anchor_id, None)
        elif isinstance(event, FinalizeResult):
            self.finalized.add(event.anchor_id)
        else:
            raise TypeError(f"unknown output event: {event!r}")

    def apply_all(self, events: Iterable[OutputEvent]) -> None:
        for ev in events:
            self.apply(ev)

    def as_dict(self) -> Dict[str, dict]:
        """{anchor_id: {"time": t, "version": v, <source>: sample_id|None}}"""
        return {
            aid: {
                "time": row.anchor_time,
                "version": row.version,
                **{m.source: (None if m.missing else m.sample_id) for m in row.matches},
            }
            for aid, row in self.rows.items()
        }
