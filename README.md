# Incremental AC-3 for Binary CSPs (Python 3.11, stdlib only)

## Files

- `ac3.py` — `CSP`: incremental AC-3 with deletion reasons, support cache,
  push/pop trail, dynamic constraint add/remove with value restoration.
- `reference.py` — independent oracle: naive full-AC fixpoint, full
  assignment enumeration, and a solution checker (shares no code with `ac3.py`).
- `test_ac3.py` — unittest suite, including randomized session
  cross-validation against the reference and enumeration.
- `demo.py` — runnable sample session.

## Properties

- Every deleted value records a `Reason`: the constraint id, the neighbor
  variable, and the neighbor's domain snapshot at deletion time (the original
  support evidence).
- The arc queue is deduplicated (`_in_queue` set) without losing rechecks:
  any domain shrink re-enqueues neighbor arcs, and a queued arc always
  re-checks against the latest domains.
- `push()` / `pop()` restore domains, the arc queue, the support cache, the
  deletion log, the constraint set and branch assignments.
- `remove_constraint` does not keep blindly shrinking: values whose deletion
  transitively depended on the removed constraint are restored and re-checked
  by AC-3, so domains end up exactly arc-consistent for the remaining
  constraints (values still unsupported are deleted again with fresh reasons).
- Empty domain => immediate conflict (`csp.conflict`, `propagate()` is False).
- `solve()` returns a full assignment verified against the original
  constraints by the independent `reference.check_solution`.

## Run

```sh
python3.11 demo.py            # sample session
python3.11 -m unittest test_ac3 -v   # tests
```
