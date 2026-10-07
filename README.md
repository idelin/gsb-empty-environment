# factstore

Bitemporal fact store in Go. Each fact carries a business valid-time
interval `[ValidFrom, ValidTo)` and a transaction sequence number `Seq`
assigned at insert time. Facts are never mutated: corrections and
deletions are new records, so every past knowledge snapshot stays
reconstructable.

## Model

A query is a 2D point lookup `Get(key, validTime, asOf)`: among the
key's records with `Seq <= asOf` whose valid interval covers
`validTime`, the one with the greatest `Seq` wins. Later corrections
therefore never leak into older snapshots, and tombstones (`Delete`)
retract only their own valid interval.

Per key, the store maintains the valid-time axis split into
non-overlapping segments; each segment keeps its winner timeline
`(TxFrom, Record)` sorted by `TxFrom`, so a point query is two binary
searches.

## API

- `Put(key, value, validFrom, validTo)` — add or correct a fact.
- `Delete(key, validFrom, validTo)` — retract over a valid interval.
- `Get(key, validTime, asOf)` — resolve a 2D point; `asOf = Now()` sees everything.
- `History(key)` / `Log()` — full traceable write history.
- `Segments(key)` — inspect the split timeline (used by tests).

## Tests

`go test ./...` runs unit tests (correction backtracking, delete
intervals, max-Seq overlap resolution, interval splitting, history) plus
a randomized cross-check against a brute-force 2D point-query reference
model over multiple seeds.
