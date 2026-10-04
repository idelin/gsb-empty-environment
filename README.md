# foldindex

A Go search-index library driven by a caller-supplied, versioned character
folding table. Rules may expand one character into many (`ß` → `ss`) or
replace a combination of characters (`...` → `…`). Hits are always reported
as intervals of the **original** document.

## Usage

```go
table, _ := foldindex.NewTable("v1", []foldindex.Rule{
    {From: "ß", To: "ss"},   // one char expands to many
    {From: "...", To: "…"},  // combined replacement
})
ix := foldindex.Build(table, "straße")
hits := ix.Search("ss") // [{Start:4 End:5 Exact:true}]
```

## Semantics

- **Interval mapping**: `Build` folds the document and records, per folded
  segment, the original rune interval it came from. A folded hit is mapped
  to the minimal original interval covering it.
- **Exact flag**: `Hit.Exact` is true only when the folded hit aligns with
  folding-unit boundaries. A query cutting through the middle of an
  expansion (e.g. `"as"` inside `ß`→`ss`) returns the covering interval
  with `Exact: false`.
- **Merging**: duplicate or overlapping hits (e.g. both `s` of `ß`→`ss`)
  are merged; a merged hit is exact only if all constituents were exact.
- **Versioning & cursors**: `SearchPage` paginates with an opaque cursor
  bound to the table version and index fingerprint. Re-versioning the
  table produces a new index; old cursors are rejected with
  `ErrStaleCursor` and must not be mixed across indexes.

All offsets are rune offsets into the original document.
