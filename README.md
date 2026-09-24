# decayfreq

Decaying frequency sketch (Count-Min style) + frequency-based admission cache,
Go standard library only.

## Design

- **Sharded sketch** (`sketch.go`): each Count-Min row is an independently
  locked shard, so concurrent `Add` calls scale. Hash seed is a fixed constant
  (`DefaultSeed`); the hasher is injectable (`WithHasher`) for collision tests.
- **Lazy epoch decay**: `Decay()` is O(1) — it bumps a global epoch; each
  counter applies pending halvings on next access. Declared logical order:
  *decay halves every counter at the moment of the call*. The lazy
  implementation is exactly equivalent to eager halving, including rounding,
  no matter how adds and decays interleave (`TestDecayInterleavingEquivalence`).
- **No negative counts, no overflow**: counters are `uint32`, decay is a
  saturating right shift, adds saturate at `MaxCount` instead of wrapping.
- **Admission cache** (`cache.go`): LRU + byte quota. A candidate is admitted
  only if its sketch estimate strictly exceeds the LRU victim's. Admission,
  eviction, and quota accounting are atomic under one mutex: victims are
  selected in a read-only phase, then committed; a rejected candidate mutates
  nothing. Frequent items are more likely retained, but estimates are
  probabilistic upper bounds — never exact frequencies.
- **Processor** (`processor.go`): data source, clock, failure injection, and
  resource release are all injected functions (`Deps`). No remote cache, no
  network. Duplicate event IDs are ignored (idempotent). Checkpoints are
  encoded to a buffer first and can be aborted by the injected failure
  function without side effects; restoring a checkpoint reproduces identical
  admission decisions.

## Run

```
go test -race ./...
go run ./cmd/demo
```

## Sample run (`go run ./cmd/demo`)

```
== top-8 decayed frequencies: exact reference vs sketch estimate ==
  key "hot-9"   exact(decayed)    109.3   estimate 109
  key "hot-5"   exact(decayed)    103.6   estimate 103
  ...
== admission cache (byte quota 120) ==
  "hot-1"    14 bytes  estimate=98
  ...
  used 120 / 120 bytes
== checkpoint 210272 bytes; decisions original [true false false], restored [true false false]
== resource released: source closed 1 time(s)
```

## Test results (`go test -race ./...`)

All 15 tests pass, including: error bound vs an independent exact reference
(max overestimate 163 vs bound 390 over 200k events), decay-interleaving
equivalence, saturation, forced collisions, atomic admission, duplicate-event
idempotency, checkpoint restore decision-equality, injected checkpoint
failure, injected-clock periodic decay, and exactly-once resource release.
