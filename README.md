# bptree — copy-on-write paged B+tree (Go standard library only)

A page-based B+tree giving export jobs a fixed-version range scan while
inserts and deletes keep flowing. int64 keys, []byte values, page capacity
as a parameter.

## Design

- **Pages.** Nodes are serialized into fixed-size pages in an in-memory page
  file (`Store`). Page size is derived from `Options.Capacity` and
  `Options.MaxValueSize`. Every page carries a CRC32 checksum;
  `Store.CheckIntegrity` audits the whole file.
- **Copy-on-write commits.** A write transaction clones every node on the
  path it touches into freshly allocated pages and atomically publishes a new
  root on commit. Old readers keep their old root and see a stable version.
  If any page write fails, nothing is published.
- **Snapshots & iterators.** `Tree.Snapshot` pins the current root; its pages
  are exempt from reclamation until `Close`. `Snapshot.Iterator(lo, hi)`
  scans `[lo, hi)`; an empty range yields an empty iterator. Iterators
  navigate between leaves through the pinned version's internal pages (path
  stack) — there are no leaf sibling links to follow into another version.
  After `Snapshot.Close`, iterator and point reads fail with
  `ErrSnapshotClosed`.
- **Deletes.** Underflow is repaired by borrowing from a sibling or merging;
  the root shrinks when reduced to one child. Separators always equal the
  minimum key of the subtree to their right (restored by a post-operation
  path repair).
- **Reclamation.** `Tree.Collect` frees exactly the pages unreachable from
  every active root (current root + open snapshots). It blocks while a write
  transaction is open.
- **Independent validator.** `Validate(store, root, capacity)` walks any
  root and checks key order, separator keys, leaf depth uniformity, node
  occupancy vs. capacity, and page references (allocated, decodable,
  referenced once).

## Layout

- `page.go` — page layout, node encode/decode, CRC
- `store.go` — page file: alloc/read/write/free, integrity check, fault
  injection and corruption helpers
- `tree.go` — tree handle, snapshot registry
- `tx.go` — copy-on-write write transaction (put/delete/split/borrow/merge,
  root grow & shrink, separator repair)
- `iterator.go` — snapshot + range iterator
- `gc.go` — reclamation
- `validate.go` — independent structural validator
- `cmd/demo` — minimal runnable sample

## Run

```sh
go test ./...        # unit + reference-model stress tests
go test -race ./...  # concurrent readers/writer/reclaimer
go run ./cmd/demo    # minimal end-to-end sample
```
