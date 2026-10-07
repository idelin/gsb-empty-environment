# tree-store

Async directory-tree state store in TypeScript. Expanding a node loads its
children asynchronously; collapse, delete and re-expand may happen before the
response arrives.

## Guarantees

- **Generation-based stale discarding**: every node carries a `generation`
  counter. `collapse()`, `remove()` and `refresh()` bump it, so a response
  captured under an older generation is dropped instead of mutating the tree.
- **One request per generation**: concurrent `expand()` calls for the same
  node generation share a single in-flight promise; the loader runs once.
- **Retryable failures that keep the displayed tree**: a rejected load sets
  `status: "error"` and records `error`, but never clears already-rendered
  children. `expand()` on an errored node issues a fresh request.
- **No ghost nodes**: `remove()` deletes the whole subtree, detaches it from
  the parent, and any late response for a removed node is discarded.
- **Selection integrity**: `selectionPath()` walks from the selected node up
  to the root. If the selected node disappears (remove or reconciled
  refresh), the selection falls back to the nearest surviving ancestor.

## API

- `new TreeStore(loader)` — `loader: (parentId: string | null) => Promise<TreeNodeData[]>`;
  `null` means the virtual root (`ROOT_ID`).
- `expand(id)` / `collapse(id)` / `refresh(id)` / `remove(id)`
- `select(id | null)` / `selectedId()` / `selectionPath()`
- `get(id)` / `rootIds()` / `childrenOf(id)`

## Tests

Tests drive the loader with controllable deferred promises
(`tests/helpers.ts`) to enumerate response orderings: stale resolve before /
after the fresh one, stale rejection, delete-before-response, and
failure-then-retry. They assert selection paths, exact load counts and the
absence of ghost nodes.

Requires Node.js >= 23.6 (or 22.6+ with `--experimental-strip-types`):

```sh
npm test
```
