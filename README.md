# Revision-safe Coverage Ledger

A Python-standard-library ledger for device-online coverage over half-open time intervals.

## Semantics

- Online reports and membership rules are half-open intervals: `[start, end)`.
- Union counts a point when at least one current group member is online.
- Intersection counts a point when every current member is online.
- `at_least_k` counts points with at least `k` distinct online members.
- Several overlapping reports from one device contribute one device, never multiple devices.
- Membership intervals carry a rule version. Observations made at an old revision can be rebuilt with both the old reports and old membership rules.
- Corrections are appended as revision events. Retracting and replacing a report creates an invertible delta for any already-published observation.

## Endpoint index

All interval endpoints are kept in one sorted index. A report or membership change updates only the segments that its interval intersects. Shared endpoints use reference counts and are removed after a revision only when their two final neighbouring states are identical. The implementation therefore does not scan every device history for a local correction; queries still iterate only the segments in their window.

## Example

```python
from coverage_ledger import CoverageLedger, Membership, Report

ledger = CoverageLedger()
ledger.add_membership(Membership("A", 0, None, "rule-v1", "mA"))
ledger.add_membership(Membership("B", 0, None, "rule-v1", "mB"))
ledger.add_report(Report("A", 0, 6, "rA"))
ledger.add_report(Report("B", 4, 8, "rB"))

union = ledger.observe(kind="union", window_start=0, window_end=10)
two = ledger.observe(kind="at_least_k", window_start=0, window_end=10, k=2)
assert union.covered_length == 8
assert two.covered_length == 2

published = union
ledger.close_membership("mB", 5)
ledger.replace_report("rB", Report("B", 3, 10, "rB-v2"))

delta = ledger.delta_for_observation(published)
corrected = delta.apply(published)
restored = delta.inverse().apply(corrected)
```

`RevisionImpact` reports both the requested event range and the actual state-changing range (`revised_scope`). Use `query_windows={"name": (kind, k, start, end)}` on `commit` to receive that actual revised scope clipped to a query window; open-ended membership tails are otherwise represented as `(start, float("inf"))`.

## Checkpoints

```python
from coverage_ledger import save_checkpoint, load_checkpoint

save_checkpoint(ledger, "ledger.checkpoint.json")
restored = load_checkpoint("ledger.checkpoint.json")
```

Checkpoint load verifies the serialized endpoint index by replaying the append-only event log.

## Tests

```sh
python -m unittest discover -s tests -v
```
