# migrate

Go library for safely normalizing file names across a tree according to
user-supplied rules, without ever overwriting a colliding path.

## How it works

1. **Full mapping first.** `BuildPlan` normalizes every name in the tree
   (per-character mapping, then case rule) and computes each node's final
   path before anything is renamed. Directories are resolved before their
   descendants, so renaming a directory shifts its whole subtree.
2. **Stable collision suffixes.** Nodes that normalize to the same target
   path are sorted by original path; the first keeps the name, the rest
   get `~2`, `~3`, ... (inserted before a file's extension).
3. **Two-phase apply.** Phase 1 renames every node to a unique temporary
   name, phase 2 renames to the final name — so name swaps and cycles
   cannot collide. `Tree.Rename` refuses to overwrite.
4. **Interruption recovery.** A plan can be persisted (`Save`/`LoadPlan`).
   `Recover` inspects each node's state (original / temp / final) and
   finishes the remaining phases; it is idempotent.

## Example

```go
tr := migrate.NewTree()
tr.Add("/My Dir/Report Final.txt", false)

rules := migrate.Rules{
    Chars: map[rune]rune{' ': '_'},
    Case:  migrate.CaseLower,
}
plan := migrate.BuildPlan(tr, rules)
if err := plan.Apply(tr); err != nil { /* handle */ }
// tr now contains /my_dir/report_final.txt
```

Run tests with `go test ./...`.
