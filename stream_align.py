"""Cross-source nearest-sample alignment with streaming corrections.

Each main-stream event is an independent anchor. For every anchor, each
auxiliary source contributes at most one sample: the nearest sample within
`tolerance`, ties broken by earlier time, then by smaller seq.

The engine is incremental: samples/anchors may arrive in any order, may be
retracted, and watermarks finalize results. Late-arriving better samples
produce `Replace` events; finalized results are never rewritten.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Tuple

NEG_INF = float("-inf")
POS_INF = float("inf")


@dataclass(frozen=True)
class Result:
    """Alignment output for one anchor."""

    anchor_id: str
    anchor_time: float
    version: int
    selections: Dict[str, Optional[str]]  # source -> sample_id (None = missing)

    @property
    def missing(self) -> Tuple[str, ...]:
        return tuple(s for s, sid in self.selections.items() if sid is None)


@dataclass(frozen=True)
class Emit:
    result: Result


@dataclass(frozen=True)
class Replace:
    result: Result


@dataclass(frozen=True)
class Retract:
    anchor_id: str


@dataclass(frozen=True)
class Finalize:
    anchor_id: str
    version: int


Event = Tuple  # Emit | Replace | Retract | Finalize


class Aligner:
    """Incremental nearest-sample aligner.

    Buffer entries are kept as sorted (time, seq, id) tuples so window
    queries and candidate scans use binary search.
    """

    def __init__(self, tolerance: float, sources: Iterable[str]):
        if tolerance < 0:
            raise ValueError("tolerance must be >= 0")
        self.tolerance = tolerance
        self.sources: Tuple[str, ...] = tuple(sources)
        self._anchors: Dict[str, Tuple[float, int]] = {}
        self._anchor_keys: List[Tuple[float, int, str]] = []  # sorted
        self._buffers: Dict[str, List[Tuple[float, int, str]]] = {
            s: [] for s in self.sources
        }
        self._by_id: Dict[str, Dict[str, Tuple[float, int]]] = {
            s: {} for s in self.sources
        }
        self._results: Dict[str, Result] = {}
        self._finalized: set[str] = set()
        self._wm: Dict[str, float] = {s: NEG_INF for s in self.sources}
        self._main_wm: float = NEG_INF

    # -- selection -----------------------------------------------------

    def _select(self, source: str, anchor_time: float) -> Optional[str]:
        tol = self.tolerance
        buf = self._buffers[source]
        lo = bisect.bisect_left(buf, (anchor_time - tol, NEG_INF, ""))
        hi = bisect.bisect_right(buf, (anchor_time + tol, POS_INF, ""))
        best_key = None
        best_id = None
        for t, seq, sid in buf[lo:hi]:
            key = (abs(t - anchor_time), t, seq)
            if best_key is None or key < best_key:
                best_key = key
                best_id = sid
        return best_id

    def _anchors_near(self, time: float) -> List[str]:
        tol = self.tolerance
        keys = self._anchor_keys
        lo = bisect.bisect_left(keys, (time - tol, NEG_INF, ""))
        hi = bisect.bisect_right(keys, (time + tol, POS_INF, ""))
        return [aid for _, _, aid in keys[lo:hi]]

    # -- anchor events -------------------------------------------------

    def add_anchor(self, anchor_id: str, time: float, seq: int = 0) -> List:
        if anchor_id in self._anchors:
            raise ValueError(f"duplicate anchor {anchor_id!r}")
        self._anchors[anchor_id] = (time, seq)
        bisect.insort(self._anchor_keys, (time, seq, anchor_id))
        sel = {s: self._select(s, time) for s in self.sources}
        res = Result(anchor_id, time, 1, sel)
        self._results[anchor_id] = res
        self._maybe_finalize(anchor_id)
        events: List = [Emit(res)]
        if anchor_id in self._finalized:
            events.append(Finalize(anchor_id, res.version))
        return events

    def retract_anchor(self, anchor_id: str) -> List:
        if anchor_id not in self._anchors:
            return []
        time, seq = self._anchors.pop(anchor_id)
        idx = bisect.bisect_left(self._anchor_keys, (time, seq, anchor_id))
        del self._anchor_keys[idx]
        self._results.pop(anchor_id, None)
        self._finalized.discard(anchor_id)
        return [Retract(anchor_id)]

    # -- sample events -------------------------------------------------

    def add_sample(
        self, source: str, sample_id: str, time: float, seq: int = 0
    ) -> List:
        if sample_id in self._by_id[source]:
            raise ValueError(f"duplicate sample {sample_id!r} in {source!r}")
        self._by_id[source][sample_id] = (time, seq)
        bisect.insort(self._buffers[source], (time, seq, sample_id))
        return self._recompute_near(source, time)

    def retract_sample(self, source: str, sample_id: str) -> List:
        ts = self._by_id[source].pop(sample_id, None)
        if ts is None:
            return []
        time, seq = ts
        buf = self._buffers[source]
        idx = bisect.bisect_left(buf, (time, seq, sample_id))
        del buf[idx]
        return self._recompute_near(source, time)

    def _recompute_near(self, source: str, time: float) -> List:
        events: List = []
        for anchor_id in self._anchors_near(time):
            if anchor_id in self._finalized:
                continue
            anchor_time = self._anchors[anchor_id][0]
            new_id = self._select(source, anchor_time)
            res = self._results[anchor_id]
            if res.selections[source] == new_id:
                continue
            sel = dict(res.selections)
            sel[source] = new_id
            res = Result(anchor_id, anchor_time, res.version + 1, sel)
            self._results[anchor_id] = res
            events.append(Replace(res))
        return events

    # -- watermarks / finalization -------------------------------------

    def advance_watermark(self, source: str, watermark: float) -> List:
        if watermark < self._wm[source]:
            return []
        self._wm[source] = watermark
        events: List = []
        for anchor_id, (anchor_time, _) in self._anchors.items():
            if anchor_id not in self._finalized and self._is_final(anchor_time):
                self._finalized.add(anchor_id)
                events.append(
                    Finalize(anchor_id, self._results[anchor_id].version)
                )
        return events

    def advance_main_watermark(self, watermark: float) -> None:
        self._main_wm = max(self._main_wm, watermark)

    def _is_final(self, anchor_time: float) -> bool:
        horizon = anchor_time + self.tolerance
        return all(self._wm[s] >= horizon for s in self.sources)

    def _maybe_finalize(self, anchor_id: str) -> None:
        if self._is_final(self._anchors[anchor_id][0]):
            self._finalized.add(anchor_id)

    # -- resource reclamation ------------------------------------------

    def gc(self) -> int:
        """Drop buffered samples no open or future anchor can still use.

        A sample at time t is dead when every anchor that could select it
        (existing open anchors, or future anchors bounded by the main
        watermark) lies outside [t - tol, t + tol].
        """
        open_times = [
            t
            for aid, (t, _) in self._anchors.items()
            if aid not in self._finalized
        ]
        floor = min([self._main_wm, *open_times])
        cutoff = floor - self.tolerance
        dropped = 0
        for source in self.sources:
            buf = self._buffers[source]
            end = bisect.bisect_left(buf, (cutoff, NEG_INF, ""))
            for t, seq, sid in buf[:end]:
                del self._by_id[source][sid]
            dropped += end
            del buf[:end]
        return dropped

    # -- introspection --------------------------------------------------

    def current_table(self) -> Dict[str, Result]:
        return dict(self._results)

    def is_finalized(self, anchor_id: str) -> bool:
        return anchor_id in self._finalized


def replay(events: Iterable) -> Tuple[Dict[str, Result], set]:
    """Consumer-side recovery: fold the event stream into a live table.

    Returns (table, finalized_anchor_ids).
    """
    table: Dict[str, Result] = {}
    finalized: set = set()
    for ev in events:
        if isinstance(ev, (Emit, Replace)):
            table[ev.result.anchor_id] = ev.result
        elif isinstance(ev, Retract):
            table.pop(ev.anchor_id, None)
            finalized.discard(ev.anchor_id)
        elif isinstance(ev, Finalize):
            finalized.add(ev.anchor_id)
        else:  # pragma: no cover
            raise TypeError(f"unknown event {ev!r}")
    return table, finalized


def align_batch(
    anchors: Iterable[Tuple[str, float, int]],
    samples: Dict[str, Iterable[Tuple[str, float, int]]],
    tolerance: float,
) -> Dict[str, Result]:
    """Independent full-scan reference implementation.

    anchors: iterable of (anchor_id, time, seq)
    samples: source -> iterable of (sample_id, time, seq)
    """
    out: Dict[str, Result] = {}
    for anchor_id, anchor_time, _ in anchors:
        sel: Dict[str, Optional[str]] = {}
        for source, items in samples.items():
            best_key = None
            best_id = None
            for sid, t, seq in items:
                dist = abs(t - anchor_time)
                if dist <= tolerance:
                    key = (dist, t, seq)
                    if best_key is None or key < best_key:
                        best_key = key
                        best_id = sid
            sel[source] = best_id
        out[anchor_id] = Result(anchor_id, anchor_time, 1, sel)
    return out
