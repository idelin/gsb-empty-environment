"""Versioned event-count engine over layered bucket indexes.

Data structures (all persistent treaps, see treap.py):

- event index: keyed by ``(ticks, seq)`` so duplicate timestamps are ordered
  by a stable insertion id; subtree counts give O(log n) rank/select for
  range counts, k-th event and quantile queries.
- bucket index: keyed by fine-bucket index, value = event count; maintained
  incrementally on every update (no full-tree rebuild).
- id index: keyed by event id, payload = ``(ticks, seq)`` so corrections and
  retractions can locate the current record.

Every mutation commits a new immutable version; old snapshots stay
queryable. A range count decomposes [lo, hi) into a partition:

- edge spans (partial fine buckets at both ends) are accounted item by item;
- fully-covered fine buckets are read from the bucket index;
- maximal runs of fine buckets that exactly tile a fully-covered business
  bucket are reported as business-layer contributions.

Each contribution records its layer, span, source, data version and schema
version, and the partition property guarantees internal full buckets and
edge details are never double counted.
"""

from __future__ import annotations

import math
from bisect import bisect_left
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

import treap
from buckets import BucketSchema
from timemodel import TimePoint


@dataclass(frozen=True)
class Contribution:
    """One piece of a layered range-count decomposition."""

    layer: str                      # 'business' | 'fine' | 'edge'
    lo: int
    hi: int
    count: int
    source: str
    schema_version: int
    data_version: int
    items: Tuple[Tuple[int, int, object], ...] = ()  # edge detail: (ticks, seq, event_id)


@dataclass(frozen=True)
class _Version:
    vid: int
    events: Optional[treap.Node]    # key (ticks, seq), payload event_id
    buckets: Optional[treap.Node]   # key bucket index, value count
    ids: Optional[treap.Node]       # key event_id, payload (ticks, seq)
    schema: int
    size: int
    next_seq: int


def _ticks_of(time) -> int:
    if isinstance(time, TimePoint):
        return time.ticks
    if isinstance(time, int):
        return time
    raise TypeError(f"expected int ticks or TimePoint, got {type(time).__name__}")


class EventEngine:
    def __init__(self, schema: BucketSchema) -> None:
        self._schemas: Dict[int, BucketSchema] = {schema.version: schema}
        self._versions: Dict[int, _Version] = {
            0: _Version(0, None, None, None, schema.version, 0, 0)
        }
        self._head = 0
        self._next_vid = 1

    # ---------------------------------------------------------- versioning

    def _commit(self, events, buckets, ids, schema_version, size, next_seq) -> int:
        vid = self._next_vid
        self._next_vid += 1
        self._versions[vid] = _Version(vid, events, buckets, ids,
                                       schema_version, size, next_seq)
        self._head = vid
        return vid

    def snapshot(self) -> int:
        """Return the current head version id; it stays queryable forever."""
        return self._head

    def _version(self, vid: Optional[int]) -> _Version:
        version = self._versions[self._head if vid is None else vid]
        return version

    def _schema_of(self, version: _Version) -> BucketSchema:
        return self._schemas[version.schema]

    # ------------------------------------------------------------- updates

    @staticmethod
    def _bump(buckets, index: int, delta: int):
        node = treap.get(buckets, index)
        new_count = (node.value if node else 0) + delta
        if new_count < 0:
            raise AssertionError("bucket count went negative")
        if new_count == 0:
            return treap.erase(buckets, index)
        return treap.insert(buckets, index, new_count)

    def add(self, event_id, time) -> int:
        """Insert an event (late arrivals welcome). Returns the new version."""
        ticks = _ticks_of(time)
        head = self._version(None)
        if treap.get(head.ids, event_id) is not None:
            raise KeyError(f"duplicate event id {event_id!r}")
        schema = self._schema_of(head)
        seq = head.next_seq
        events = treap.insert(head.events, (ticks, seq), 1, event_id)
        ids = treap.insert(head.ids, event_id, 1, (ticks, seq))
        buckets = self._bump(head.buckets, schema.leaf_bucket(ticks), +1)
        return self._commit(events, buckets, ids, schema.version,
                            head.size + 1, seq + 1)

    def correct(self, event_id, new_time) -> int:
        """Move an event to a corrected time, keeping its stable seq id."""
        new_ticks = _ticks_of(new_time)
        head = self._version(None)
        node = treap.get(head.ids, event_id)
        if node is None:
            raise KeyError(f"unknown event id {event_id!r}")
        old_ticks, seq = node.payload
        schema = self._schema_of(head)
        events = treap.erase(head.events, (old_ticks, seq))
        events = treap.insert(events, (new_ticks, seq), 1, event_id)
        ids = treap.insert(head.ids, event_id, 1, (new_ticks, seq))
        buckets = self._bump(head.buckets, schema.leaf_bucket(old_ticks), -1)
        buckets = self._bump(buckets, schema.leaf_bucket(new_ticks), +1)
        return self._commit(events, buckets, ids, schema.version,
                            head.size, head.next_seq)

    def retract(self, event_id) -> int:
        """Remove an event entirely."""
        head = self._version(None)
        node = treap.get(head.ids, event_id)
        if node is None:
            raise KeyError(f"unknown event id {event_id!r}")
        ticks, seq = node.payload
        schema = self._schema_of(head)
        events = treap.erase(head.events, (ticks, seq))
        ids = treap.erase(head.ids, event_id)
        buckets = self._bump(head.buckets, schema.leaf_bucket(ticks), -1)
        return self._commit(events, buckets, ids, schema.version,
                            head.size - 1, head.next_seq)

    def update_schema(self, schema: BucketSchema) -> int:
        """Switch the head to new bucket rules.

        Old versions keep their old schema and remain queryable. The bucket
        index for the new schema is derived once from the event index; event
        updates afterwards are incremental again -- the tree is not rebuilt
        on every update.
        """
        if schema.version in self._schemas:
            raise KeyError(f"schema version {schema.version} already registered")
        self._schemas[schema.version] = schema
        head = self._version(None)
        buckets = None
        current_index: Optional[int] = None
        current_count = 0
        for (ticks, _seq), _value, _payload in treap.iterate(head.events):
            index = schema.leaf_bucket(ticks)
            if index != current_index:
                if current_count:
                    buckets = treap.insert(buckets, current_index, current_count)
                current_index, current_count = index, 0
            current_count += 1
        if current_count:
            buckets = treap.insert(buckets, current_index, current_count)
        return self._commit(head.events, buckets, head.ids, schema.version,
                            head.size, head.next_seq)

    # ------------------------------------------------------------- queries

    def size(self, vid: Optional[int] = None) -> int:
        return self._version(vid).size

    def items(self, vid: Optional[int] = None) -> Tuple[Tuple[int, int, object], ...]:
        """All live events as (ticks, seq, event_id), in count-index order."""
        version = self._version(vid)
        return tuple((key[0], key[1], payload)
                     for key, _value, payload in treap.iterate(version.events))

    def _edge_items(self, version: _Version, lo: int, hi: int
                    ) -> Tuple[Tuple[int, int, object], ...]:
        return tuple((key[0], key[1], payload)
                     for key, _value, payload
                     in treap.iterate(version.events, (lo, -1), (hi, -1)))

    def range_count(self, lo, hi, vid: Optional[int] = None
                    ) -> Tuple[int, List[Contribution]]:
        """Exact count of events in [lo, hi) plus its layered decomposition."""
        lo, hi = _ticks_of(lo), _ticks_of(hi)
        if lo >= hi:
            raise ValueError("empty range: lo must be < hi")
        version = self._version(vid)
        schema = self._schema_of(version)
        width = schema.fine_width
        contributions: List[Contribution] = []

        def edge(span_lo: int, span_hi: int) -> None:
            items = self._edge_items(version, span_lo, span_hi)
            if items:
                contributions.append(Contribution(
                    "edge", span_lo, span_hi, len(items),
                    f"edge items of bucket {schema.leaf_bucket(span_lo)}",
                    schema.version, version.vid, items))

        def fine(start_idx: int, end_idx: int, layer: str, source: str) -> None:
            if start_idx > end_idx:
                return
            count = treap.range_total(version.buckets, start_idx, end_idx + 1)
            if count:
                span_lo, _ = schema.bucket_span(start_idx)
                _, span_hi = schema.bucket_span(end_idx)
                contributions.append(Contribution(
                    layer, span_lo, span_hi, count, source,
                    schema.version, version.vid))

        first_full = -((-lo) // width)      # ceil(lo / width)
        last_full = hi // width - 1         # bucket i full iff (i+1)*w <= hi
        if first_full > last_full:
            edge(lo, hi)                    # range inside partial buckets only
        else:
            edge(lo, min(hi, first_full * width))
            edge(max(lo, (last_full + 1) * width), hi)
            self._layered(version, schema, first_full, last_full, lo, hi,
                          contributions, fine)
        contributions.sort(key=lambda c: (c.lo, c.hi))
        return sum(c.count for c in contributions), contributions

    def _layered(self, version, schema, first_full, last_full, lo, hi,
                 contributions, fine) -> None:
        """Decompose full buckets [first_full..last_full] into business/fine."""
        width = schema.fine_width
        bounds = schema.business_boundaries
        cursor = first_full
        idx = bisect_left(bounds, lo)
        while idx + 1 < len(bounds) and bounds[idx + 1] <= hi:
            start_idx = bounds[idx] // width
            end_idx = bounds[idx + 1] // width - 1
            fine(cursor, start_idx - 1, "fine",
                 f"fine buckets [{cursor}..{start_idx - 1}]")
            fine(start_idx, end_idx, "business",
                 f"business bucket [{bounds[idx]}, {bounds[idx + 1]})")
            cursor = end_idx + 1
            idx += 1
        fine(cursor, last_full, "fine",
             f"fine buckets [{cursor}..{last_full}]")

    def kth(self, k: int, vid: Optional[int] = None) -> Tuple[int, object]:
        """The k-th event (1-based) in (ticks, seq) order: (ticks, event_id)."""
        version = self._version(vid)
        node = treap.select(version.events, k - 1)
        return node.key[0], node.payload

    def quantile(self, q: float, vid: Optional[int] = None) -> int:
        """Smallest ticks t such that at least ceil(q*n) events are <= t."""
        if not 0.0 <= q <= 1.0:
            raise ValueError("q must be in [0, 1]")
        version = self._version(vid)
        if version.size == 0:
            raise ValueError("quantile of an empty version")
        k = min(version.size, max(1, math.ceil(q * version.size)))
        return self.kth(k, version.vid)[0]
