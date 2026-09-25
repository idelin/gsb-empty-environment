# Recorded run results

Command (run 2026-09-25, Node v22.14.0, macOS arm64):

```sh
node --experimental-strip-types --test test/*.test.ts
```

## Test summary (actual output)

```
ok 1 - cost benchmark over the full enumeration of fetch orders
ok 2 - two concurrent evaluations share one fetch; cancelling one does not affect the other
ok 3 - cache is merged per subject+scope and isolated across permission scopes
ok 4 - an evaluation cancelled before the first flush never fetches anything
ok 5 - reentrant request for an in-flight attribute fails deterministically
ok 6 - reentrant request for an unrelated attribute is served normally
ok 7 - a fetch that never resolves fails with FetchTimeoutError
ok 8 - mixed-version reads are retried until the read set is version-consistent
ok 9 - a version that keeps switching fails deterministically with VersionConflictError
ok 10 - partial batch failure retries only the failed attributes, then succeeds
ok 11 - a permanently failing attribute yields AttributeUnavailableError without affecting others
ok 12 - deny decided by a false cheap condition never requests the sensitive expensive field
ok 13 - planner distinguishes must-read from optional attributes
ok 14 - decision and access evidence are saved atomically as one record
# tests 14
# pass 14
# fail 0
# duration_ms ~1400-2100
```

## Benchmark diagnostics (actual output)

Scenario 1 — three-condition `and`, costs a=1, b=5, c=20, domains {0,1,2},
27 assignments enumerated exhaustively; totals are exact fetch-cost sums.
The scheduler's real fetch totals match the analytic model exactly.

```
order [a, b, c]  total cost 237  <- heuristic (cheapest-first)
order [b, a, c]  total cost 264
order [b, c, a]  total cost 318
order [a, c, b]  total cost 417
order [c, a, b]  total cost 579
order [c, b, a]  total cost 588
optimal total 237; heuristic total 237; gap 0
```

Scenario 2 — costs d=1, e=2; d passes 9/10 of the time, e passes 1/10.
Here cheapest-first is strictly worse than the enumerated optimum, which is
exactly why the heuristic is NOT claimed to be globally minimal.

```
order [e, d]  total cost 210
order [d, e]  total cost 280  <- heuristic (cheapest-first)
optimal total 210; heuristic total 280; gap 70
```
