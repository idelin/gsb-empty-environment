# softcache

Soft/hard TTL cache in Go (standard library only) with coalesced
refreshes, failure backoff and an injectable clock.

## Semantics

For each key, a stored value has a soft expiry and a hard expiry:

| window                    | behavior                                                        |
|---------------------------|-----------------------------------------------------------------|
| `now < softExp`           | serve the cached value (fresh)                                  |
| `softExp <= now < hardExp`| serve the cached value with `Stale=true`, trigger one coalesced background refresh |
| `now >= hardExp`          | never serve the old value; wait for the shared refresh, return the new value or an error |

- Soft-expired `Result` carries the source `Version` and `UsableUntil`
  (the hard expiry of the served value).
- Refresh coalescing: at most one in-flight refresh per key; hard-miss
  readers share it.
- Failure backoff: a failed refresh opens a backoff window
  (`Backoff(failures)`) that suppresses new attempts. Backoff never
  extends the hard expiry — hard-expired readers get the recorded error,
  not the stale value.
- A refresh result is stored only if its version is newer; a late,
  older response is discarded and released, so it can neither overwrite
  a newer value nor resurrect a deleted key.

## Injection points

Everything external is an injectable function — no remote cache client:

- `Source`: `func(ctx, key) (value, version, error)` — data source and
  its failures; `ctx` is cancelled on `Close`.
- `Now`: clock (tests use a controllable fake clock).
- `Backoff`: `func(failures) time.Duration` — failure policy.
- `Release`: `func(value)` — called exactly once for every value the
  cache stops owning (overwritten, invalidated, closed, or discarded
  late/stale fetch), so borrowed resources are always released.

## Linearization points

All under `Cache.mu`:

1. **Get hit** — reading the entry (fresh or stale) under the lock.
2. **Hard miss** — joining or creating the per-key `refreshCall` under
   the lock; this is what guarantees a single shared refresh.
3. **Refresh commit** — `runRefresh` re-checks the key generation and
   the version under the lock before storing. `Invalidate` bumps the
   generation under the same lock, so a racing refresh either commits
   entirely before the invalidation (and is discarded) or entirely
   after it (and is stored).
4. **Reader cancellation** — a hard-miss waiter selects on `ctx.Done()`;
   the shared refresh keeps serving the remaining waiters.
5. **Close** — sets `closed` under the lock, releases all stored values,
   cancels the source context, and waits for in-flight refreshes, whose
   late results are discarded and released.

## Run

```sh
go test -race ./...   # tests, incl. fake-clock state exploration
go run ./cmd/demo     # deterministic demo with a manual clock
```
