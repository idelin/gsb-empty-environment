# idemexec

Idempotent request execution for Go: a request is executed **exactly
once** per idempotency key, even across crashes, lost receipts, retries,
and concurrent duplicates.

## Semantics

`Executor.Do(key, params, fn)`:

- **Atomic effect+result.** `fn` stages its business effects on the
  transaction `Tx`; the effects and the result record commit in the same
  atomic transaction. A crash can never persist one without the other, so
  a lost receipt is always recoverable by retrying the same key.
- **Replay.** A duplicate call with the same key and parameters gets the
  cached result; `fn` does not run again.
- **Parameter guard.** Reusing a key with different parameters fails with
  `ErrParamsMismatch` (parameters are compared by SHA-256 fingerprint).
- **Tombstone.** After the result TTL lapses, `Sweep` (or the next access)
  converts the record to a tombstone: the result payload is dropped, but
  the key and fingerprint are kept forever. Any further call fails with
  `ErrTombstoned` — the effect can never be re-executed. Tombstones are
  never deleted.
- **Failed executions are not recorded.** If `fn` returns an error the
  transaction aborts and the key stays free for a retry.

## Persistence cut points

`MemStore` commits through two crashable cut points:

- `CutBeforeApply` — staged writes not yet durable (crash ⇒ nothing
  happened; retry re-executes).
- `CutAfterApply` — writes durable, receipt not yet delivered (crash ⇒
  lost receipt; retry replays the cached result).

`Injector` arms a crash at the n-th cut-point hit; `IsCrash` identifies
the injected panic so callers can restart and retry.

## Verification

`simulation_test.go` enumerates the persistence cut points of the full
request lifecycle (execute → retry → expire → sweep → replay attempts).
For every cut point it crashes there, restarts on the same durable state,
and asserts the invariants:

- the business effect is applied **exactly once** (durable counter == 1);
- every delivered receipt is identical;
- after expiry the record is a clean tombstone (payload dropped, key
  retained) and replay is permanently rejected;
- sweeping is idempotent and never deletes tombstones.

Concurrency tests cover duplicate stampedes, racing parameter sets
(exactly one commits, losers get `ErrParamsMismatch`), and stampedes with
a lost receipt mid-flight.

## Usage

```go
store := idemexec.NewMemStore(nil)
exec := idemexec.NewExecutor(store, 24*time.Hour)

result, err := exec.Do("order-123", canonicalParams, func(tx idemexec.Tx) ([]byte, error) {
    // stage business effects on tx; they commit atomically with the result
    tx.EffectPut("orders", "order-123", []byte("confirmed"))
    return []byte("receipt-42"), nil
})
```

Run the tests:

```sh
go test -race ./...
```
