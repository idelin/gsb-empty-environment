# wsample

Streaming weighted random sampling without replacement for Go.

A `Sampler` maintains a mutable finite set of items, each with a positive
integer weight, and draws `k` **distinct** items. The probability model is
sequential: at each draw, item `i` is chosen with probability
`w_i / (sum of weights of items not yet drawn)`.

## Features

- **Streaming updates**: `Add` (insert or update weight), `Remove` (revoke,
  leaving no residual weight), and `Apply` for `Event` streams.
- **Exact semantics**: weights are integers held in a Fenwick tree, so draws
  are `O(k log n)` and totals are always exact.
- **Injectable randomness**: any `Source` (`Int63n`) works — including
  `*rand.Rand` from the standard library.
- **Exportable state**: the bundled `SplitMix64` generator exposes
  `State`/`SetState`; restoring a state reproduces successive draws exactly.

## Example

```go
rng := wsample.NewSplitMix64(42)
s := wsample.New(rng) // or wsample.New(rand.New(rand.NewSource(42)))

s.Add("a", 1)
s.Add("b", 2)
s.Apply(wsample.Event{ID: "c", Weight: 3})
s.Apply(wsample.Event{ID: "b", Remove: true})

picked, err := s.Sample(2) // 2 distinct items from {a, c}

state := rng.State() // export; rng.SetState(state) restores the stream
```

## Testing

`go test ./...` verifies the exact probability distribution on small sets by
enumerating every possible random-integer script and comparing outcome
probabilities as exact rationals — no statistical flakiness.
