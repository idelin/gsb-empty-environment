# GIN index with tri-state scanning

`prescreen.local/ps-31f1927a0b` is an importable Go package (`gin`) that
provides an in-memory generalized inverted index for array/JSONB-style keys.
It combines a fast-update pending list, logical deletion versions, ordered
posting trees, and a tri-state query executor with lossy page bitmaps.

## Quick start

```go
package main

import (
	"fmt"

	gin "prescreen.local/ps-31f1927a0b"
)

func main() {
	idx := gin.New(
		gin.WithPendingCapacity(128),
		gin.WithPostingTreeThreshold(64),
		gin.WithMaxExactTIDsPerScan(1024),
	)
	defer idx.Flush()

	doc1 := gin.MakeTID(1, 1)
	doc2 := gin.MakeTID(1, 2)

	_ = idx.Insert(doc1, "go", "db")
	_ = idx.InsertJSONB(doc2, []byte(`{"tags":["go","gin"],"user":{"name":"alice"}}`))

	for _, match := range idx.Query(gin.And("tags[]=go", "user.name=alice")) {
		fmt.Printf("tid=%d recheck=%v\n", match.TID, match.Recheck)
	}
}
```

TIDs use a compact 48-bit page number plus 16-bit offset encoding:

```go
tid := gin.MakeTID(page, offset)
page := gin.TIDPage(tid)
offset := gin.TIDOffset(tid)
```

## Write path

- `Insert` / `InsertKeys` append `(key, tid)` pairs to the pending list.
- Reusing a TID creates a new logical version. Old pending entries and old main
  postings are ignored immediately, then purged by the next flush. This
  prevents a deleted TID from being resurrected.
- `Flush` atomically drains the pending list under one write lock. Queries see
  either the complete state before the flush or after it.
- When a key's posting exceeds `PostingTreeThreshold`, it is upgraded from a
  sorted posting list to an ordered AVL posting tree. Flush removes duplicate
  and deleted TIDs.
- Exceeding `PendingCapacity` triggers a synchronous atomic flush during the
  insert.

## Query path

Predicates are tri-state expressions built from `Key`, `And`, `Or`, `Not`,
`Subset`, or `Custom`:

```go
predicate := gin.And(
	gin.Key("go"),
	gin.Not(gin.Key("deprecated")),
)
matches := idx.Query(predicate)
```

Predicates that contain `Not` scan all live TIDs, because a complement cannot
be generated from positive postings alone. A custom predicate can opt into the
same behavior:

```go
predicate := gin.Custom(
	[]string{"a", "b"},
	func(lookup gin.KeyLookup) gin.Truth {
		return gin.TriAnd(lookup("a"), gin.TriNot(lookup("b")))
	},
	gin.WithFullScan(true),
)
```

`Query` also accepts a key set and a map-style consistency function directly:

```go
matches = idx.Query([]string{"a", "b"}, func(states map[string]gin.Truth) gin.Truth {
	return gin.TriOr(states["a"], states["b"])
})
```

Kleene semantics are used throughout:

| expression | result |
| --- | --- |
| `FALSE AND MAYBE` | `FALSE` |
| `TRUE AND MAYBE` | `MAYBE` |
| `TRUE OR MAYBE` | `TRUE` |
| `NOT MAYBE` | `MAYBE` |

## Lossy pages

`MaxExactTIDsPerScan` bounds exact TIDs cached by one scan. When the candidate
set exceeds the limit, the densest heap pages are converted to page-only lossy
entries until the exact set fits. For a lossy page:

- every key with a posting on that page is propagated as `MAYBE`;
- `FALSE` predicate results prune the page without a heap visit;
- `MAYBE` results return every live TID on the page with `Recheck: true`;
- `TRUE` results need no recheck.

`Match.Lossy` identifies the source, `Match.Recheck` says whether the caller
must verify the heap tuple, and `Evaluate` can be used for an exact recheck.

## Arrays and JSONB

`InsertArray` flattens scalar arrays. `InsertJSONB` accepts JSON bytes, a JSON
string, or a marshalable Go value. JSONB keys include:

- object paths, for example `user.name`;
- plain scalar tokens, for example `alice`;
- path-qualified tokens, for example `user.name=alice`;
- array path and element tokens, for example `tags[]` and `tags[]=go`.

Use `JSONBKeys` or `ExtractJSONBKeys` to inspect the generated vocabulary.

## Verification

Run:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The test suite covers pending/flushed TID equivalence, repeated
delete/reinsert, posting-list upgrades, lossy-page pruning and recheck flags,
array/JSONB extraction, custom predicates, randomized reference comparison,
and concurrent readers/writers.
