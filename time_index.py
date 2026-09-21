"""Two-level time aggregation index with correction and retraction support.

Events carry integer timestamps and numeric values.  Two aggregate levels
are maintained eagerly: a fine level (default width 5) and a coarse level
(default width 20, must be a multiple of the fine width).  Buckets are
located by floor division, so negative timestamps behave consistently
(t = -1 with width 5 lands in bucket -1, covering [-5, 0)).

Range queries over [lo, hi) decompose the interval into a partition:

* maximal run of fully covered coarse buckets  -> coarse aggregates
* remaining head/tail, refined into fully covered fine buckets
  -> fine aggregates
* the two residual edge slivers (each narrower than one fine bucket)
  -> raw events

Because the decomposition is a partition of [lo, hi), every matching
event contributes exactly once.

Writes, corrections and retractions update both aggregate levels inside a
single method call.  All validation happens before any mutation, so a
failed operation leaves the index untouched, and a finished call always
leaves both levels consistent with the raw event store -- a reader can
never observe one level already updated and the other still stale.
"""

from __future__ import annotations

from typing import NamedTuple


def _ceil_div(a: int, b: int) -> int:
    """Ceiling division that is also correct for negative ``a``."""
    return -(-a // b)


class QueryResult(NamedTuple):
    total: float
    agg_nodes: int    # aggregate buckets (fine + coarse) actually read
    edge_events: int  # raw events read in the residual edge slivers


class TwoLevelTimeIndex:
    def __init__(self, fine_width: int = 5, coarse_width: int = 20) -> None:
        if fine_width <= 0 or coarse_width <= 0:
            raise ValueError("window widths must be positive")
        if coarse_width % fine_width != 0:
            raise ValueError("coarse width must be a multiple of fine width")
        self.fine_width = fine_width
        self.coarse_width = coarse_width
        # event_id -> (time, value); the source of truth.
        self._events: dict[int, tuple[int, float]] = {}
        # bucket index -> summed value; zero-sum buckets are removed so the
        # tables stay sparse (empty fine windows inside a coarse window
        # simply do not appear).
        self._fine: dict[int, float] = {}
        self._coarse: dict[int, float] = {}

    # ------------------------------------------------------------------
    # mutation
    # ------------------------------------------------------------------
    @staticmethod
    def _bucket_add(table: dict[int, float], bucket: int, delta: float) -> None:
        new_value = table.get(bucket, 0) + delta
        if new_value == 0:
            table.pop(bucket, None)
        else:
            table[bucket] = new_value

    def _apply(self, t: int, value: float, sign: int) -> None:
        """Apply one signed contribution to both aggregate levels."""
        delta = value * sign
        self._bucket_add(self._fine, t // self.fine_width, delta)
        self._bucket_add(self._coarse, t // self.coarse_width, delta)

    def add(self, event_id: int, t: int, value: float) -> None:
        if event_id in self._events:
            raise KeyError(f"duplicate event id {event_id!r}")
        self._events[event_id] = (t, value)
        self._apply(t, value, +1)

    def update(self, event_id: int, t: int, value: float) -> None:
        """Correct an event in place or move it to a new timestamp."""
        old_t, old_value = self._events[event_id]  # validates before mutating
        self._apply(old_t, old_value, -1)
        self._apply(t, value, +1)
        self._events[event_id] = (t, value)

    def remove(self, event_id: int) -> None:
        """Retract an event, unwinding both aggregate levels."""
        old_t, old_value = self._events.pop(event_id)
        self._apply(old_t, old_value, -1)

    def get(self, event_id: int) -> tuple[int, float]:
        return self._events[event_id]

    def __len__(self) -> int:
        return len(self._events)

    # ------------------------------------------------------------------
    # query
    # ------------------------------------------------------------------
    def query(self, lo: int, hi: int) -> float:
        """Sum of values of events with lo <= t < hi."""
        return self.query_with_stats(lo, hi).total

    def query_with_stats(self, lo: int, hi: int) -> QueryResult:
        if lo >= hi:  # empty range
            return QueryResult(0, 0, 0)

        total = 0
        agg_nodes = 0
        edge_events = 0

        # Coarse buckets [ck_lo, ck_hi) are fully covered by [lo, hi):
        # bucket k covers [k*C, (k+1)*C), so k >= ceil(lo/C) and
        # (k+1)*C <= hi  <=>  k < hi//C.
        ck_lo = _ceil_div(lo, self.coarse_width)
        ck_hi = hi // self.coarse_width
        for k in range(ck_lo, ck_hi):
            if k in self._coarse:
                total += self._coarse[k]
                agg_nodes += 1

        if ck_lo < ck_hi:
            segments = [(lo, ck_lo * self.coarse_width),
                        (ck_hi * self.coarse_width, hi)]
        else:
            segments = [(lo, hi)]

        for seg_lo, seg_hi in segments:
            if seg_lo >= seg_hi:
                continue
            t, a, e = self._fine_level_sum(seg_lo, seg_hi)
            total += t
            agg_nodes += a
            edge_events += e

        return QueryResult(total, agg_nodes, edge_events)

    def _fine_level_sum(self, lo: int, hi: int) -> tuple[float, int, int]:
        """Sum [lo, hi) via fine aggregates plus raw edge events."""
        total = 0
        agg_nodes = 0
        edge_events = 0

        fk_lo = _ceil_div(lo, self.fine_width)
        fk_hi = hi // self.fine_width
        if fk_lo < fk_hi:
            for k in range(fk_lo, fk_hi):
                if k in self._fine:
                    total += self._fine[k]
                    agg_nodes += 1
            edges = [(lo, fk_lo * self.fine_width),
                     (fk_hi * self.fine_width, hi)]
        else:
            edges = [(lo, hi)]

        for edge_lo, edge_hi in edges:
            if edge_lo >= edge_hi:
                continue
            for t, value in self._events.values():
                if edge_lo <= t < edge_hi:
                    total += value
                    edge_events += 1

        return total, agg_nodes, edge_events


if __name__ == "__main__":
    index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
    samples = [(1, 3, 10), (2, 4, 20), (3, 7, 5), (4, 12, 6),
               (5, 19, 7), (6, 21, 8), (7, 22, 9), (8, 30, 100)]
    for event_id, t, value in samples:
        index.add(event_id, t, value)

    result = index.query_with_stats(3, 23)
    print(f"query [3, 23): total={result.total} "
          f"agg_nodes={result.agg_nodes} edge_events={result.edge_events}")

    # Move the boundary event at t=4 out of the range.
    index.update(2, 40, 20)
    result = index.query_with_stats(3, 23)
    print(f"after moving event 2 (t=4 -> t=40): total={result.total} "
          f"agg_nodes={result.agg_nodes} edge_events={result.edge_events}")
