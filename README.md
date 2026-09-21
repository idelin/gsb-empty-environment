# Temporal Graph Evidence Queries

Pure-Python implementation using only the standard library.

## Semantics

- `simultaneous_paths(snapshot, source, target)` returns `ConcurrentPath` values where every selected edge is valid at one common integer departure time. Each result carries the intersected half-open interval and immutable edge records.
- `earliest_journey(snapshot, source, target, start_time, horizon)` returns either `None` or a `Journey` with concrete `JourneyStep` evidence. Waiting is allowed at a vertex, except during that vertex's blocked half-open intervals.
- `earliest_journey_expanded(...)` is an independent oracle that explicitly builds small-integer `(vertex, time)` nodes and wait/travel arcs.
- `verify_concurrent_path(...)` and `verify_journey(...)` check every reported step against edge ID/version, source, target, window, duration, waiting, and blocked vertices.

Snapshots bind a query to an immutable revision. Edge corrections and withdrawals allocate a new per-edge version. `EvidenceCache` revokes only entries that depended on the replaced edge or vertex version; old entries remain readable through old snapshots.

## Run

```sh
python3 sample.py
python3 -m unittest discover -s tests -v
```
