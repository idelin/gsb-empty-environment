# sfm — symbol frequency & conditional frequency models

Standard-library-only Go module for incremental frequency modelling,
MDL model-structure selection, and a real byte-level codec. No
compression libraries are used; everything is implemented here.

## Layout

- `freq/` — order-0 `Model` and order-1 `CondModel`, smoothing, entropy
  estimates, MDL structure selection (exhaustive for small domains).
- `codec/` — canonical Huffman coding and the versioned, checksummed
  stream format.
- `cmd/sfmdemo/` — runnable end-to-end demo (`go run ./cmd/sfmdemo`).

## Explicit rules

- **Smoothing**: add-k, `p(s) = (count(s)+K) / (total+K*alpha)`,
  `K = DefaultK = 0.5` (Krichevsky-Trofimov). Every symbol in the
  declared alphabet has probability > 0 — this is the escape strategy
  for zero-frequency / unknown symbols. Symbols outside the declared
  alphabet are rejected by the encoder (`ErrUnknownSymbol`).
- **Zero frequency**: never fed to `log2`; zero-count symbols contribute
  0 to entropy sums. Deterministic sequences have entropy exactly 0.
- **Estimates vs. reality**: `Entropy`/`CodeLength` are floating point
  estimates only. Real byte lengths come from `codec.Encode*`.
- **Retraction**: `Retract` of a sample that is not present fails with
  `ErrNotPresent`. Totals always equal adds − retracts (conservation).
- **Context merging**: `MergeContexts` sums two contexts into one
  shared, reference-counted table. Any mutation through a context that
  shares its table copies it first (copy-on-write), so retracting or
  deleting via one context can never pollute another. `SplitContext`
  detaches a context from a shared table.
- **MDL**: total cost = fixed header + context→group assignment bits
  (`nctx*log2(groups)`) + per-group parameter bits
  (`alpha*ParamBitsPerCount`) + smoothed data cost. `SelectExhaustive`
  enumerates all set partitions for ≤ 8 contexts.

## Stream format

```
'S' 'F' 'M'  VERSION(0x01)  flags(0=order0, 1=order1)
uvarint alphabetSize
uvarint symbolCount          (decode is bounded: must be <= caller limit)
uvarint numGroups
per group: uvarint context, alphabetSize bytes of code lengths
uint32 CRC-32/IEEE of everything above (big endian)
payload bits, MSB-first, zero-padded to a byte
```

Decoding rejects: bad magic, unknown version/flags, checksum mismatch,
truncation, trailing garbage, and declared outputs above the caller's
limit.

## Test

```
go test ./...
```
