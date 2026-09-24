# Coalescing Invalidation Cache (Go, standard library only)

An invalidation-driven refresh coalescer. Many failure/invalidation signals for
the same key are **merged into one rate-limited load**, critical keys jump the
queue, and correctness is guaranteed even under back-to-back invalidations,
full queues, slow in-flight loads, stale data-source versions and load errors.

## Guarantees

1. **Merged invalidations, one load** — a key already queued or in flight is
   not enqueued again. A thousand invalidations of one key produce one load.
2. **Rate limited** — a single worker dispatches at most one batch per
   `Interval`; each batch holds at most `BatchSize` keys.
3. **Critical keys first** — `InvalidateCritical` uses a priority lane; the
   worker drains that lane before the normal FIFO lane. Promoting an already
   queued key only changes its lane (no duplicate load).
4. **Invalidation watermark (generation)** — every invalidation bumps a
   monotonic generation. The snapshot of generations taken at dispatch time is
   checked at response time: a load confirms a key **only if no newer
   invalidation arrived while it was in flight**, otherwise the response is
   discarded and the key re-queued. Merged invalidations therefore never lose
   the latest one.
5. **Version watermark** — the invalidation version only rises; a data-source
   response with a lower version than the recorded watermark is never accepted
   (a lagging/old leader cannot resurrect stale data).
6. **Bounded queue, conservative invalidation** — when the queue is full the
   key is marked invalid immediately and recorded in a deferred set; its old
   value stops serving right away. It is admitted automatically (critical
   lane first) as soon as space frees up.
7. **Failures are never confirmed** — on loader error or a missing per-key
   result the keys stay invalid and are retried on later ticks; the optional
   injected `OnError` sink is notified.
8. **No remote cache dependency** — the cache holds only local state; the data
   source is an injected function.
9. **Resource release** — `Close` cancels the in-flight load, stops the ticker
   and joins the worker goroutine; it is idempotent.

## Injectable dependencies

- `Config.Load` — `func(ctx, []K) (map[K]LoadResult[V], error)` data source.
- `Config.Clock` — `Clock` (`Now`, `NewTicker`). Use `RealClock` in production
  and the exported `ManualClock` in tests for deterministic ticking.
- `Config.OnError` — failure sink.

## API

```go
c := coalesce.New(coalesce.Config[string, string]{
    Load:          loadFromDB, // injected data source
    Clock:         coalesce.NewRealClock(),
    Interval:      10 * time.Millisecond,
    BatchSize:     16,
    QueueCapacity: 1024,
    OnError:       func(keys []string, err error) { /* report */ },
})
defer c.Close()

_ = c.Invalidate("user:1", 23)         // normal lane, version 23
_ = c.InvalidateCritical("user:1", 23) // priority lane
v, ver, err := c.Get("user:1")         // ErrInvalid when no validated value
```

## Layout

- `clock.go` — `Clock`/`Ticker`, `RealClock`, deterministic `ManualClock`.
- `coalesce.go` — cache, lanes, watermarks, worker, lifecycle.
- `coalesce_test.go` — property tests (run under `go test`, with `-race`).
- `cmd/demo` — deterministic end-to-end sample driven by a manual clock.

## Run

```sh
go vet ./...
go test -race -v ./...
go run ./cmd/demo
```

## Test coverage

- 1000 repeated invalidations -> exactly 1 data-source call.
- 1000 invalidations during an in-flight load -> in-flight response dropped,
  exactly one covering retry.
- Full queue -> old value immediately `ErrInvalid`, then auto-recovery.
- In-flight invalidation -> successful load confirms only its own generation.
- Data-source version below watermark -> rejected until the source catches up.
- Critical keys precede normal keys inside rate-limited batches.
- One batch per interval tick; no bursts.
- Injected failure -> nothing confirmed, `OnError` fired, retry succeeds.
- `Close` releases goroutine and ticker, cancels in-flight loads, rejects
  further use, and is idempotent.
