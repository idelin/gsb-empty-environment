# tree-alias-mounts

A TypeScript tree library with stable node ids, bounded-hop path aliases, and
read-only virtual mounts.

## Model

- **Tree** (`src/tree.ts`): nodes have stable ids; moving a node updates its
  real path. Nothing in the library ever rewrites a node id.
- **Aliases** (`src/alias.ts`): an alias maps a path prefix to a target node
  id. Resolution is a bounded-hop interpreter: each hop rewrites the longest
  matching alias prefix to the target's current real path, stopping at a
  fixed point, when no prefix matches, or when the hop budget is exhausted
  (`CycleError`). Adding an alias that would create a resolution cycle is
  rejected and rolled back; same-prefix redefinition is an
  `AliasConflictError`; resolving through a deleted target raises
  `DeletedTargetError`. Moving a node never rewrites alias target ids.
- **Mounts** (`src/mount.ts`): an explicit graph of virtual mounts, each with
  an independent root and a `readOnly` flag. The graph may contain cycles —
  concrete paths are finite, so walking them terminates — and `..` can never
  escape above a mount root (`RootEscapeError`). Mount ownership is intrinsic
  to the subtree (nearest mount-root ancestor), so aliases cannot smuggle
  writes across a read-only boundary.
- **Transactions** (`src/transaction.ts`): `prepare()` resolves source and
  target identities first, then validates; `commit()` applies the move. Any
  failure leaves the tree unchanged, and a configuration switch between
  prepare and commit aborts with `StaleConfigError`.

## Develop

```sh
npm test   # type-checks, compiles to dist/, and runs the test suites
```
