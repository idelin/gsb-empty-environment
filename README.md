# Cross-source nearest-sample alignment

`sample_alignment` aligns every primary-stream event as an independent anchor. For each configured auxiliary stream, it keeps the nearest retained sample within that stream's tolerance.

Selection order is:

1. smaller absolute timestamp distance;
2. on equal distance, earlier sample timestamp;
3. on an exact timestamp tie, smaller `seq`;
4. finally, smaller sample ID for deterministic tests.

## Incremental API

```python
from sample_alignment import (
    AlignmentStore,
    AnchorAdded,
    SampleAdded,
    WatermarkAdvanced,
)

store = AlignmentStore(["alt"], 2)
store.process_event(SampleAdded("s8", "alt", 8, 1))
store.process_event(AnchorAdded("m10", 10, 1))
store.process_event(SampleAdded("s12", "alt", 12, 2))  # no replacement: 8 wins the tie
store.process_event(SampleAdded("s9", "alt", 9, 3))   # emits an upsert correction
store.process_event(WatermarkAdvanced(12))            # finalizes m10
```

Methods `add_sample`, `retract_sample`, `add_anchor`, `retract_anchor`, and `advance_watermark` are available directly. `process_event` handles one event object; `process_events` provides the batch entry point. Both return the same ordered `AlignmentEvent` values.

Per-source tolerances can be provided as a mapping:

```python
AlignmentStore(["gps", "imu"], {"gps": 2, "imu": 1})
```

## Results and corrections

Each `AlignmentResult` contains:

- `anchor_id`, `anchor_seq`, and `anchor_timestamp`;
- `matches`: ordered `(source, sample_id)` pairs;
- `missing_sources`: sources without an in-tolerance candidate;
- `version`: per-anchor result version;
- `finalized`: watermark status.

`AlignmentEvent` is either an `upsert` or `delete`. Consumers can maintain the current table by keying rows on `result.anchor_id`, replacing on `upsert`, and removing on `delete`. `previous_version` identifies the row being corrected.

Watermarks close an anchor at `anchor_timestamp + max(tolerance)`. Retractions and late samples do not emit changes for finalized rows.

## Retention and garbage collection

A candidate is retained while either condition holds:

- it is within tolerance of an active anchor;
- its timestamp-specific horizon (`sample_timestamp + source_tolerance`) has not passed the watermark, so a future anchor may still use it.

This means one candidate can serve many independent anchors, and removing one anchor cannot remove a candidate still referenced by another open anchor.

## Verification and costs

- Unit and randomized property tests: `python3 -m unittest discover -s tests -v`
- Consumer replay demonstration: `python3 -m examples.correction_replay_demo`
- Microbenchmark: `python3 -m benchmarks.benchmark_alignment --count 2000 --width 8`

Let `W` be the number of anchors within a sample's tolerance window, `C` the candidates in one source, and `K` candidates sharing a timestamp. Candidate and anchor insertion use sorted lists, so list insertion is `O(C + A)`; window selection is `O(log C + K)`; sample arrival/retraction emits at most `O(W)` corrections. Finalization and reference-count garbage collection are charged when anchors close or watermarks advance. High fan-out input intentionally costs `O(W)` because every changed anchor receives an independently versioned correction.
