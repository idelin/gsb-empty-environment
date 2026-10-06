# kvdb

In-memory integer-keyed store with serializable optimistic transactions.

## Design

- Transactions read committed state and record **half-open range
  predicates** (`Scan(start, end)` records `[start, end)`) and **point
  predicates** (`Get`), plus their **write set** (`Put`/`Delete`).
- Predicates are recorded even when a scan returns no rows, so phantoms
  are prevented.
- At `Commit`, the transaction validates against the write sets of every
  transaction committed after its snapshot:
  - a committed write to a key covered by any read predicate (range or
    point) aborts the commit — the read result would have changed;
  - a committed write to a key in the transaction's own write set aborts
    the commit (first committer wins).
- Writes outside every read predicate never conflict and always succeed.
- Committed write sets are pruned once no open transaction can still
  validate against them.

## Verification

`go test ./...` runs:

- unit tests for inserts/updates/deletes inside vs. outside a predicate
  range, boundary-adjacent intervals (`[0,10)` vs `[10,20)`), empty-range
  phantom protection, point reads, write/write conflicts;
- a serializability check that enumerates every commit order of up to
  four concurrent transactions and verifies the committed history equals
  the serial re-execution of the committed transactions in commit order.
