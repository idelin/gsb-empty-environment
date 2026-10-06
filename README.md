# migrate

Go library that normalizes file names in a virtual file tree according to a
user-supplied per-character mapping and case rule, without ever overwriting
colliding paths.

## How it works

1. **Full mapping first** — `ComputePlan` normalizes every base name in the
   tree up front. Renaming a directory rewrites the final paths of all its
   descendants.
2. **Collision groups** — nodes whose final paths collide are ordered by
   original path; the first keeps the normalized name, the rest get stable
   numeric suffixes (`report~2.txt`, `dir~2`). A node that does not move
   still occupies its path, so anything moving onto it is suffixed.
3. **Two-phase rename** — `Apply` moves every affected node to a unique
   temporary name (phase 1), then to its final name (phase 2), so swaps and
   ancestor/descendant renames never overwrite each other.
4. **Crash recovery** — the plan plus a phase-1 checkpoint are journaled
   into the tree itself (`.migrate-journal`). After an interruption at any
   point, `Recover` (or re-running `Apply`) finishes the migration
   idempotently.

## Usage

```go
tr := migrate.NewTree()
tr.Add("/Reports/My File.TXT", false)

norm := migrate.Normalizer{
    CharMap: map[rune]string{' ': "_"},
    Case:    migrate.CaseLower,
}
if err := migrate.Apply(tr, norm); err != nil { ... }
if err := migrate.Recover(tr); err != nil { ... } // after a crash
```

## Tests

```
go test ./...
```

The suite runs entirely against the virtual file tree and covers name swaps
(same level and across levels), ancestor collisions, collision-group
suffixing, directory renames propagating to descendants, and recovery from
crashes before/during phase 1, between phases, and mid phase 2.
