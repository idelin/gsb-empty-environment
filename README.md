# vfs-alias-tree

A TypeScript tree library with stable node ids, path aliases, and virtual
mount points with read-only boundaries. Resolution is a bounded-jump
interpreter; moves are transactional.

Requires Node.js >= 23.6 (native type stripping). No dependencies.

```sh
npm test
```

## Model

- **Tree** (`src/tree.ts`): nodes have stable ids that never change across
  moves. `realPath(id)` is derived from the current parent chain.
- **Aliases** (`src/aliases.ts`): alias path -> target node id. Targets are
  ids, so moving a node updates its real path but never rewrites alias
  targets; removing a node leaves a dangling alias that fails resolution
  with `ALIAS_TARGET_MISSING`. Exact duplicate alias paths are rejected
  (`ALIAS_CONFLICT`); overlapping prefixes coexist and the longest prefix
  wins.
- **Resolver** (`src/resolver.ts`): bounded-jump interpreter. Each alias
  hit rewrites the path to the target's current real path and costs one
  jump; resolution stops after `fuel` jumps (`ALIAS_RESOLUTION_LIMIT`) or
  on a revisited path (`ALIAS_CYCLE`). `setAlias` validates with the same
  interpreter, so aliases that would form resolution cycles (including
  self-shadowing ones) are rejected at configuration time and rolled back.
- **Mounts** (`src/mounts.ts`): explicit mount graph; each mount grafts an
  independent root node at a virtual path with an optional `readOnly`
  flag. A mount whose root contains its own graft point is rejected
  (`MOUNT_CYCLE`). `..` may not pop above a mount root (`ROOT_ESCAPE`).
  Write checks run against the *resolved* node's owning mount, so aliases
  cannot bypass read-only boundaries.
- **Transactions** (`src/txn.ts`): `move` resolves source/destination
  identities first, validates, and stages the mutation; `commit`
  re-validates and applies all staged ops. If the alias/mount
  configuration version changed since `beginTx`, commit throws
  `CONFIG_CHANGED`. A failed transaction never modifies the tree.

## Tests

- `test/aliases.test.ts`: longest-prefix priority, nested aliases and fuel
  limits, deleted targets, same-prefix conflicts, cycle rejection, and
  move-keeps-alias-target-id semantics.
- `test/mounts.test.ts`: mount-graph cycles and parent tracking, root
  escape, read-only enforcement through aliases, cross-mount aliases,
  transactional atomicity, and config-switch-after-check aborts.
