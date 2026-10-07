# ttlcache

A Go TTL cache built on an **explicit monotonic clock**: time never advances
on its own, callers move it with `Advance(d)`. Expiration is therefore
deterministic, testable, and safe to persist and restore.

## Design

- **Explicit monotonic time** — the cache holds its own clock (starting at
  zero); `Set` deadlines are `now + ttl`, and `Advance` fires expiry events
  stamped with each entry's true deadline.
- **Generations** — every `Set` starts a new generation for the key. Delete
  and expire events carry the generation they target, so a stale event from
  an older generation can never delete a newer value.
- **Persistence** — `Snapshot()` records each live entry's *remaining*
  lifetime and the save time. `Restore(records, downtime)` subtracts the
  downtime from every remaining lifetime; entries that are not positive
  expire immediately (logged as expire events) and are never re-granted a
  full TTL. The generation counter resumes above all restored generations.
- **Event replay** — every mutation is appended to an event log;
  `Replay(events, now)` folds the log up to a given monotonic time and
  reproduces the live state, which the tests use to verify generation
  isolation and restore behavior.

## Test

```sh
GO111MODULE=on go test ./...
```
