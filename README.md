# pagereloc

Offline page-file compaction and relocation tool. Compaction drops free
slots so page numbers change; every in-file reference is rewritten through
a generated relocation table while external stable object IDs are preserved
unchanged.

## Usage

```
pagereloc -in old.bin -out new.bin -table reloc.json
```

## File format

- 32-byte file header: magic `PGRLC001`, version, page size, slot count,
  root page number.
- Fixed-size page slots; a free slot is all zeros. An occupied slot stores
  an 8-byte external stable object ID (0 = none), a reference list (page
  numbers), and an opaque payload.

## Guarantees

- **Sharing preserved**: the page graph may be an arbitrary DAG. Shared
  subpages are copied once via a memoized old→new map, never silently
  duplicated or split.
- **Bad graphs rejected**: cycles, dangling references (out-of-range or
  free-slot targets), and duplicate external IDs abort the run.
- **Relocation table**: JSON record of every old→new page mapping plus an
  external-ID index so external systems keep resolving stable object IDs.
- **Validated before publish**: the new file and table are written to
  temporary files, fsynced, re-read from disk, and checked by an
  independent traversal (`ValidateEquivalence`) proving content
  equivalence, dense renumbering, and absence of dangling references. Only
  then are they atomically renamed into place (data first, table last —
  the table is the commit marker). A crash mid-write leaves previously
  published outputs untouched and no temp files behind.

## Development

```
go test ./...
```

Tests cover sparse page numbers, shared subpages, cyclic/corrupt graphs,
tamper detection by the validator, crash injection at every byte offset of
the temp write and at every pipeline stage, and stale temp recovery.
