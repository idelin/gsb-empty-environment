# arc

Go implementation of an Adaptive Replacement Cache (ARC).

The cache splits its capacity between two live queues:

- **T1** — entries accessed exactly once (first-access queue)
- **T2** — entries accessed at least twice (repeat-access queue)

Keys evicted from either queue are remembered as valueless **ghost
records** in **B1** / **B2**. A request that hits a ghost record shifts
the target size `p` of T1 up (B1 hit) or down (B2 hit); `p` is always
clamped to `[0, capacity]`. Capacity is accounted in **entries**: every
key counts as one, regardless of the size of its value.

## Usage

```go
c := arc.New(2)
c.Set("a", 1)          // insert into T1
c.Get("a")             // hit: promote to T2
c.Set("b", 2)          // evicts to a ghost list when full
c.Delete("a")          // removes live entries and ghost records
c.Len()                // live entries (T1 + T2)
c.Target()             // current target p, 0 <= p <= Capacity()
c.Snapshot()           // "p=1 T1=[...] T2=[...] B1=[...] B2=[...]"
```

`Get` on a ghost record reports a miss; adaptation happens when the key
is stored again via `Set`. The cache is not safe for concurrent use.

## Testing

`go test ./...` runs scripted traces (capacity one, repeated deletes,
target adjustment), a printed per-step example, and a differential test
that replays thousands of random operations against a naive slice-based
reference model, requiring identical queue states at every step.
