# kvindex

In-memory Go index for a key-value store that accepts **point writes** and
**half-open range deletes**, with visibility decided by **monotonic sequence
numbers**.

## Semantics

- `Put(key, value, seq)` — point write.
- `DeleteRange(start, end, seq)` — tombstones the half-open range
  `[start, end)`; `start` is deleted, `end` is not.
- Sequence numbers must be monotonic:
  - `seq < lastSeq` → `ErrNonMonotonicSeq`.
  - `seq == lastSeq` with a *different* operation → `ErrConflictingOp`.
  - `seq == lastSeq` with the *identical* operation → idempotent no-op.
- A newer point write resurrects a key covered by an older tombstone.

## Visibility model

Each key owns an ascending event history (put / tombstone) stamped with
sequence numbers. A key is visible at sequence `q` iff the newest event with
`seq <= q` is a put. Because sequence numbers are monotonic, a range delete
only affects keys already present, so it is materialized as one tombstone
event per existing key in the range — later writes carry higher sequence
numbers and land after the tombstone.

## Reads

- `Get(key, seq)` — historical point query at any past sequence number.
- `Scan(ScanOptions{Start, End, After, Seq, Limit})` — range scan over
  `[Start, End)` at `Seq`; returns a continuation token (`next`) to pass
  back as `After` for the next page. Concatenating all pages equals the
  unlimited scan.

## Tests

`go test ./...` runs deterministic checks (nested tombstones, half-open
endpoints, resurrection, duplicate-sequence rejection, pagination
continuation) plus a randomized differential test that cross-checks the
index against an independent replay-the-log reference model.
