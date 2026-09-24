# Priority Path Reservation

Python 3.11, standard library only.

## Model

- `Network`: undirected graph with string vertex/edge IDs; isolated
  vertices and parallel multi-edges are preserved.
- `Demand`: `(id, priority, source, target, bandwidth, interval)`;
  intervals are half-open `[start, end)`.
- Capacity is tracked per edge as interval reservations; feasibility
  checks the peak overlapping load inside the requested interval.

## Behavior

- `reserve_all(demands)`: reserves a fixed demand list ordered by
  priority (desc), ties broken by demand ID (asc). If capacity fits
  only one of two competing demands, only one is committed.
- `reserve_path(demand, path)`: acquires edges one by one; on midway
  failure all already-acquired edges are released (rollback).
- `fail_edge(edge_id, demands)`: reroutes only demands whose path
  used the edge; demands that cannot be rerouted stay unreserved
  (old reservation rolled back).
- `recover_edge(edge_id)`: idempotent; capacity derives from the
  reservation list, so recovery never double-adds capacity.

## Run

```
python3 demo.py        # sample scenario
python3 -m unittest -v # tests
```
