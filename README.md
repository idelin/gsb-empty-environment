# Temporal graph queries

The library uses only the Python standard library.

- `QueryEngine.simultaneous_paths(source, target, snapshot_id=None)` returns every simple path whose edges are all open at one common integer time. A result route contains exact `EdgeVersion` legs and the complete half-open interval for which that combination is valid.
- `QueryEngine.simultaneous_at(source, target, time, snapshot_id=None)` returns concrete `RideStep` values at one query time.
- `QueryEngine.earliest_journey(source, target, time, snapshot_id=None)` returns an earliest-arrival sequence of `WaitStep` and `RideStep` values under the waiting semantics. Waiting cannot overlap a vertex blocked interval, and arrival/departure during a blocked interval is forbidden.
- Graph mutations return a new snapshot id. Old snapshots remain immutable and readable. Cache entries retain exact edge id/revision and per-vertex adjacency-set dependencies; correcting or withdrawing an edge removes only evidence depending on that edge or an affected adjacency set.
- `validate_simultaneous_route` and `validate_journey` check every returned step against the exact edge records in the bound snapshot.

Run the demo:

```sh
python3 demo.py
```

Run all tests, including an independent expanded-time oracle and endpoint/segment checks:

```sh
python3 -m unittest -v
```
