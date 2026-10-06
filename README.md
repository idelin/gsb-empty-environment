# mask

Deterministic, type-preserving masking of selected fields in JSON documents.

```go
store := mask.NewFileStore("mapping.json")
m := mask.New(store, []string{"email", "phone"}, mask.WithKey("secret"))
out, err := m.Mask(srcJSON)
```

## Guarantees

- **Deterministic**: the same original value always maps to the same
  replacement, across documents, files, and process restarts (via the
  persistent mapping table).
- **Type & null preservation**: strings mask to strings, integers to
  integers, floats to floats, booleans to booleans; `null` is copied
  untouched and never mapped.
- **Byte-identical passthrough**: the document is rewritten at the token
  level; every byte outside the selected fields (whitespace, key order,
  escapes, number formatting) is copied verbatim.
- **Collision handling**: replacements are derived from a keyed digest.
  When two distinct originals collide, the persistent table allocates a
  deterministic suffix (`"tok_x#1"` for strings, appended digits for
  numbers, the opposite value for booleans).
- **Transactional**: all mappings needed by one document are committed in
  a single atomic `Store.Commit` (temp file + rename for `FileStore`).
  A failure discards the whole transaction — no partial mappings are
  published, and a crash mid-commit leaves the previous table intact.
  Version conflicts (`ErrConflict`) are retried with a fresh snapshot.

## Field selection

Fields are matched by object key at any depth. When a field matches, its
scalar value is masked; arrays and objects under it have all their scalar
descendants masked. Object keys are never masked.

## Testing notes

`WithDigestFunc` injects the digest used to derive replacements; a
constant digest forces every value onto the same candidate, which the
tests use to exercise suffix allocation, table recovery, and
transaction-failure paths.

Run tests (Go 1.11):

```sh
GO111MODULE=on go test ./...
```
