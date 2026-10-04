# async-stage-channels

A small TypeScript library that connects capacity-limited processing stages
with bounded async channels. A paused consumer progressively blocks upstream
pulls (backpressure), stage failures abort the whole chain, and every
transferred element is released exactly once by its current owner.

## Layout

- `src/elements.ts` — `TrackedElement` (numbered, `release()`-able) and
  `ElementTracker` (records every release, detects leaks and double releases).
- `src/channel.ts` — `BoundedChannel`: `send()` waits while full,
  `receive()` resolves `undefined` once closed and drained, `close(error)`
  fails pending receivers/senders fast, `drain()` returns leftover items.
- `src/pipeline.ts` — `Pipeline`: producer → N stages → sink, one bounded
  channel per link.

## Ownership rules

1. The creator owns an element until `send()` completes.
2. The channel owns buffered elements.
3. The receiving stage owns elements returned by `receive()` and must
   release them (after processing, or immediately when aborting).
4. On shutdown the pipeline drains every channel and releases the leftovers
   on behalf of the downstream stage (`cleanup:<stage>` in the release log).

Stage outputs must be fresh elements created via `ctx.create(value)`; a
stage's input is released by the pipeline once the stage function returns.

## Failure semantics

- A stage throwing (or the sink throwing) aborts the chain: all channels
  close with the error, blocked sends/receives fail, in-flight elements are
  released by their owners, and `run()` rejects with the original error.
- A sink returning `false` stops the chain gracefully; `run()` resolves
  with `{ consumerStopped: true }` after full cleanup.

## Tests

Requires Node.js >= 22.6 (TypeScript type stripping) or any `tsc` toolchain.

```sh
./run-tests.sh   # auto-detects a suitable Node (incl. ~/.nvm/versions)
```

Covers: ordered delivery, backpressure bounds while the consumer is paused,
cleanup integrity after a mid-stage failure, and cleanup after an early
consumer exit — each verified via the tracker's release log (every numbered
element released exactly once).
