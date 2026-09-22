# rle-history

Run-length encoded device-state history with source indexing (Python 3.11+).

- Consecutive equal states collapse into one `Segment` over a half-open
  interval `[start, end)`; the last segment is open-ended (`end=None`).
- Every raw event keeps its `(source, seq)` identity in `Segment.members`;
  splits and merges never drop evidence.
- Edits (`insert` / `retract` / `correct`) return an incremental `Patch`
  (`removed` + `added` segments) so downstream consumers can maintain a
  segment view without the raw events — see `examples/patch_consumer.py`.

## Ordering / conflict rule

Events are canonically ordered by `(time, source, seq)`. When several events
share a timestamp, the one sorting last decides the state at that instant;
earlier same-time events stay as evidence in a (possibly zero-length)
segment. No event is ever dropped from the source index.

## API

```python
from rle_history import StateHistory, apply_patch

h = StateHistory()
patch = h.insert("dev", 1, 0, "A")     # -> Patch
patch = h.retract("dev", 1)            # -> Patch
patch = h.correct("dev", 1, time=2, state="B")  # -> Patch
h.segments()        # direct query
h.state_at(t)       # state at integer time t
h.provenance(t)     # raw EventRefs forming the segment covering t
view = apply_patch(view, patch)        # consumer side
```

## Tests

`pytest` cross-checks three paths in every scenario: direct query, initial
snapshot + replay of all patches, and an independent naive recomputation
from the raw events. Covered: split/heal of the A segment around B@1,
first/last retraction, same-time conflicts, single record, empty stream.
