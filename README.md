# arccache

Go implementation of an Adaptive Replacement Cache (ARC).

- **Two value queues**: T1 holds keys accessed once, T2 holds keys accessed
  more than once. A hit in T1 promotes the key to T2.
- **Ghost records**: keys evicted from T1/T2 linger in B1/B2 without values.
  A hit on a ghost record adapts the target size `p` of T1 (T2's target is
  implicitly `capacity - p`).
- **Entry-count accounting**: capacity is the number of live entries
  (T1 + T2); ghosts are bookkeeping only and never count against it.
- **Clamped target**: `p` is always kept within `[0, capacity]`.

## Usage

```go
c := arc.New(128)
c.Set("key", value)
if v, ok := c.Get("key"); ok { ... }
c.Delete("key") // repeated deletes are safe no-ops
```

## Demo

`go run ./cmd/arcdemo` runs a scripted session and prints `p`, T1, T2, B1
and B2 after every step.

## Tests

`go test ./...` runs targeted tests (capacity 1, repeated deletes, target
adjustment and clamping) plus a randomized cross-check that drives the cache
and a naive slice-based reference model with identical operation sequences
and requires identical state at every step.
