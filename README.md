# Segmented leases for pooled protocol buffers

The original bug: a binary parser lends sub-slices of a pooled buffer to async
consumers, then the pool reuses that buffer for another tenant. A slow consumer
or stale reference can therefore read the next tenant's data.

This repo fixes it with **generation-tagged, reference-counted segmented
leases** so a buffer is never reused while a reference is live and reads
through a stale reference are rejected.

## Components

- `lease/pool.go` — a tiny fixed-size pool. Every checkout bumps the physical
  buffer's `gen`. `Acquire` blocks on a `sync.Cond` while all buffers are pinned
  and returns `ctx.Err()` when canceled.
- `lease/lease.go` — one `Lease` per checkout plus share-owned `Segment`s.
- `parser/parser.go` — a length-prefixed (2-byte big-endian) frame parser that
  decodes into leased segments and hands frame ownership to consumers.

## Invariants

- **Shared ownership.** The owner holds one reference; each derived segment
  holds one. The buffer stays pinned until the last reference is released.
- **Exactly-once zeroing.** Cancellation, timeout, normal release and segment
  release may all race; `sync.Once` plus a root-release guard ensure the buffer
  is zeroed and returned to the pool at most once. `Pool.Clears()` exposes the
  counter used to assert this.
- **Reads after return are rejected.** Once the owner releases/cancels/times
  out, or a segment is released, `Segment.Bytes()` returns
  `lease.ErrLeaseClosed` instead of data.
- **Generation defense-in-depth.** Each buffer carries a generation that is
  bumped on checkout; a captured old segment whose buffer was recycled under a
  newer generation cannot read it, even if it bypasses the closed flag.
- **Tenant isolation.** Before a buffer is handed to a new tenant it is fully
  zeroed, so the new tenant only ever sees zeroes in reused regions.

Note on Go semantics: a raw `[]byte` captured before termination cannot be
forcibly invalidated by the runtime, so callers must always access payloads
through `Segment.Bytes()` / `Frame.Payload()`, never retain the returned slice
past release.

## Verification

```sh
go test -race -count=5 ./...
go test -cover ./...
```

Key tests:

- `TestBlockingConsumerMinimalPool` — pool of one buffer; the consumer blocks
  reuse, post-return reads fail, and the next tenant reads zeroes only.
- `TestConcurrentReleasePathsZeroOnce` — 200 iterations of cancel + timeout +
  release + duplicate segment release; asserts exactly one zero per lease.
- `TestTimeoutRejectsReadsButPinsBuffer` — timeout racing normal release.
- `TestSegmentSharedOwnership`, `TestAcquireCanceledWhilePinned`,
  `TestGenerationMismatchRejectsStaleReference`, `TestSegmentRangeGuards`.
- Parser tests cover split frames, oversized frames, truncated streams and
  end-to-end cross-tenant isolation.
