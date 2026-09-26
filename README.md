# efano — Elias-Fano encoding for non-decreasing uint64 sequences

Pure Go (standard library only). Supports duplicates, O(log n) random
access and `lower_bound` with exact ranks, bounded-memory streaming
construction, direct high/low-level merge of two encoded sequences, and
a self-describing, independently verifiable byte format.

## Encoding layout

For `n` values with universe `u = max+1` and low width
`l = max(0, floor(log2(u/n)))`:

```
value[i] = (high[i] << l) | low[i]

low  : n*l bits — the l low bits of every value, packed contiguously.
high : n + floor((u-1)/2^l) bits — for each value, (high[i]-high[i-1])
       zero bits followed by a one bit (unary gaps).
       Hence high[i] = select1(i) - i.
rank : one uint64 per 512 high bits (select/rank directory, ~1.6%).
```

This is the real encoded layout — no hash maps, no materialized arrays.

## Parameter choice and space derivation

Total size as a function of `l`:

```
T(l) = n*l  (low bits)  +  n  (unary ones)  +  floor((u-1)/2^l)  (unary zeros)
```

Treating `l` as continuous, `dT/dl = n - ln2 * u / 2^l = 0` gives the
optimum `2^l = (ln 2) * u/n`, i.e. `l* = log2(u/n) - 0.47`. The integer
choice `l = floor(log2(u/n))` (what `ChooseL` returns) is within 1 of
the optimum and forces the zero count into `[n, 2n)`, so

```
T <= n*log2(u/n) + 3n  bits   =>   <= log2(u/n) + 3 bits per value
```

i.e. at most ~3 bits above the `log2(u/n)` bits of entropy per element.
Degenerate inputs are safe by construction: `n == 0` or `u <= n`
(including `max < n`) yields `l = 0` — no division by zero, no low bits.
Because the universe only ever appears as `u/2^l`, astronomical upper
bounds (e.g. values near `2^64`) enlarge the low field, not the high
vector, so memory stays `O(n * log(u/n))` bits with no `O(u)` allocation.

## Operations

- `At(i)` — `select1(i) - i` for the high part, direct packed read for
  the low part; `select1` via binary search over the rank directory.
- `LowerBound(x)` — exact rank (count of values `< x`). Splits `x` into
  `(hi, lo)`, jumps to the unary bucket for `hi` via zero-select, then
  binary-searches the bucket's low bits. `x > max` returns `Len()`
  (past-the-end). Empty index returns 0.
- `EqualRange(x)` / `Count(x)` — exact half-open rank range and
  multiplicity of duplicates.
- `Builder.Add(v)` — streaming, O(1) state beyond the compressed
  output; input is never buffered.
- `Merge(a, b)` — walks both high vectors bit by bit and both low
  vectors at packed offsets, emitting merged unary gaps and low bits
  directly. No full decode; O(1) extra memory. Requires equal `l`.

## Verifiable serialized format

48-byte little-endian header (`magic "EFAN"`, version, `l`, `n`, max,
high/low bit lengths, reserved-zero fields, CRC-32/IEEE over header +
payload), then the high and low byte images. `Parse` rejects anything
with bad magic/version/reserved bytes, wrong total length,
`lowBits != n*l`, high population `!= n`, non-zero padding, or CRC
mismatch — before any allocation sized by header fields, so corrupt or
hostile inputs cannot cause huge allocations. `Verify()` independently
re-decodes and checks monotonicity and the declared maximum.

## Run

```
go test ./...      # full test suite (see below)
go run ./cmd/efdemo
go test -run Example -v   # testable usage example
```

Tests cover: element-wise comparison against an independent reference
array, `lower_bound`/`EqualRange` ranks vs `slices.BinarySearch`, random
single-bit mutation detection (500 flips, all rejected), an exact byte
ledger checked against the format formula, the `ChooseL` space bound
over 10k random parameters, streaming-vs-batch equivalence, merge
(incl. cross-sequence duplicates), empty sets, all-equal values,
`max < n` (`l = 0`), and past-the-end queries.
