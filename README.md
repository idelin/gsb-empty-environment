# sparsearray

Sparse array in Go (stdlib only): a logical array of length `n` where almost
every position holds a default value. Only exception positions and pending
lazy range-add layers are stored physically.

## Semantics

- `Set(p, v)` — point update; stores an explicit exception. An explicit `0`
  is a real exception (`IsException` reports it) and is never confused with
  a missing position.
- `RangeAdd(lo, hi, d)` — O(1) lazy layer; huge default regions are never
  iterated. A later `Set` at a position overrides earlier layers there
  (watermark per exception).
- `RangeSum(lo, hi)` — includes the arithmetic default contribution
  `default * (len - exceptionsInRange)` plus per-layer contributions of
  non-exception positions.
- `SetDefault(newDef, followOld)` — unassigned positions follow the new
  default (0 -> 2 makes them read 2). `followOld` decides whether exceptions
  equal to the OLD default follow too; exceptions equal to the NEW default
  are always reclaimed. `SetDefault` first folds pending layers into the
  exceptions (O(E*L)) and clears the layer list — it is the compaction
  point, so query cost stays proportional to the layers since the last
  compaction.
- `Snapshot()` — O(1) copy-on-write frozen view; position identity and
  history are preserved across later mutations.
- `Locate(k)` — smallest `p` with prefix sum `[0,p] >= k`; only open on
  non-negative snapshots (monotone prefix sums), closed conservatively once
  a negative value/delta/default appears (`RevalidateNonNegative` can
  reopen it).

## Physical encoding (see `sparse/encode.go`)

uvarint / zigzag-varint throughout; positions delta-encoded:

```
uvarint n | zigzag default | byte nonNeg | uvarint seq
uvarint L | L * (uvarint lo, uvarint hi-lo, zigzag delta, uvarint seq)
uvarint E | E * (uvarint posDelta, zigzag val, uvarint watermark)
```

Physical size is O(E + L), independent of `n`. Measured at `n = 2^40`
(8 TiB dense) with 10 000 exceptions and 90 layers: **65 623 bytes**
(~6.6 bytes/exception).

## Complexity

| op          | cost                              |
|-------------|-----------------------------------|
| Set         | O(E) sorted insert, O(log E) hit  |
| RangeAdd    | O(1)                              |
| RangeSum    | O(E_in_range * L + L log E)       |
| SetDefault  | O(E * L), folds + clears layers   |
| Locate      | O(log n) RangeSum queries         |
| Snapshot    | O(1)                              |

Measured at `n = 2^40`, E=10 000, L=90 (darwin/amd64, go1.11):
RangeSum ~1.5 ms/op (1/4 span), RangeAdd ~0.35 µs/op, Set ~22 µs/op,
Locate ~118 ms/op.

## Run

```
go test ./...        # tests incl. random reference validation + benchmarks
go run ./cmd/demo    # walkthrough + large-scale evidence
```
