# alertjoin

Keyed interval join over two alert streams (left/right): two events match
when they share a `Key` and their event-time difference is at most `d`.
Each matching pair is emitted exactly once, when the later event arrives.

## Semantics

- **Dedup**: an event ID re-delivered on the same side with identical
  content is ignored; with different content, `AddEvent` returns a
  `*ConflictError`.
- **Late events**: after `UpdateWatermark(s, w)`, events on side `s` with
  `Time <= w` violate the watermark contract and are dropped silently.
  The two sides' watermarks may advance independently (unsynchronized);
  non-monotonic updates are ignored.
- **State reclamation** (per side, two kinds of state):
  - match state is reclaimed once the *opposite* side's watermark passes
    `event.Time + d` — no future event from that side can match it;
  - dedup state is reclaimed once the side's *own* watermark passes
    `event.Time` — any later re-delivery is late and dropped anyway.

## Testing

`joiner_test.go` covers boundary (`|dt| == d`), duplicate/conflict,
eviction timing, and late events, plus a randomized property test
(300 seeds) that feeds out-of-order events, duplicates, conflicts and
unsynchronized watermarks, then checks the emitted pairs against a
brute-force Cartesian-product filter over the deduplicated inputs.

```sh
go test ./...
```
