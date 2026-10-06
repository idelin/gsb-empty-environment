# outline-move

Multi-select drag-and-drop moves for outline trees, as a small dependency-free
TypeScript library.

## Model

An outline document is a forest of `{ id, children }` nodes. A drop target is
`{ parentId, index }` where `parentId: null` means the root level and `index`
is expressed in **pre-removal coordinates**: it refers to the target parent's
children array as it was *before* the dragged nodes are taken out.

## Behaviour

- **Multi-select, non-adjacent nodes** — any set of ids can be moved in one call.
- **Ancestor absorbs descendants** — when a selected node's ancestor is also
  selected, the descendant is ignored (it travels with the ancestor).
- **Self/descendant drops rejected** — moving a node into itself or into its
  own subtree fails with `target-inside-selection`; a target parent that is
  itself selected is rejected the same way.
- **Source order preserved** — moved nodes are inserted in source document
  (pre-order) order, regardless of the order they were clicked.
- **Atomic failure** — `moveNodes` is pure: on any validation error it returns
  the original tree reference untouched; on success the input is not mutated.
- **Undo** — `OutlineHistory` keeps immutable snapshots, so `undo()`/`redo()`
  restore exact previous states.

## API

```ts
import { moveNodes, normalizeSelection, OutlineHistory } from './src/index.ts';

const result = moveNodes(tree, ['A1', 'A3'], { parentId: 'C', index: 0 });
if (result.ok) {
  // result.tree is the new forest; result.movedIds is in document order
} else {
  // result.error: 'empty-selection' | 'unknown-node' | 'unknown-target-parent'
  //             | 'target-inside-selection' | 'index-out-of-range'
}

const history = new OutlineHistory(tree);
history.move(['A1'], { parentId: null, index: 0 });
history.undo(); // restores the exact previous tree
```

Verification helpers: `flattenPreOrder` (document-order flattening with parent
links) and `isAncestor` (parent-chain check), both also used by the test suite.

## Tests

Requires a Node version with type stripping (Node 22.18+ / 23.6+ works out of
the box):

```sh
npm test
```

The suite covers same-parent moves (including pre-removal index adjustment),
cross-parent moves, ancestor/descendant selection absorption, drops onto
selected targets, failure atomicity, and undo/redo restoration.
