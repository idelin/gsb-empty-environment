# canvas-paste

TypeScript library for pasting a copied subgraph onto a canvas, with
transactional public id allocation and undo/redo.

## Requirements

Node.js >= 23.6 (runs TypeScript directly via type stripping; no build
step and no dependencies).

## Behavior

`pasteSubgraph(clipboard, graph, allocator)`:

- Every copied node gets a fresh id from the public sequence.
- **Internal references** (target inside the copied set) are rewired to
  the corresponding new node.
- **External references** are kept only when the target exists in the
  destination graph and its runtime type matches the slot's declared
  `targetType`; otherwise the slot is marked `dangling` (with the
  original target id and the reason: `missing-target` or
  `type-mismatch`).
- The paste is atomic: on any failure the graph is left untouched and
  the public id sequence is not consumed (`IdAllocator` only advances
  its cursor on transaction commit).

`History` + `PasteCommand` provide undo/redo. Undo removes exactly the
inserted nodes; redo re-inserts the same nodes, so it never allocates
new ids and restores the identical state.

`isIsomorphicPaste(source, pasted, idMap)` verifies that a paste
preserves the source structure: a bijection on ids, identical node
types, identical internal wiring modulo the id mapping, and external
refs either kept verbatim or explicitly marked dangling.

## Layout

- `src/graph.ts` — node/ref model and the `Graph` container (atomic `addAll`)
- `src/id-allocator.ts` — transactional public id sequence
- `src/paste.ts` — `pasteSubgraph`
- `src/history.ts` — `History` and `PasteCommand`
- `src/isomorphism.ts` — structural isomorphism check
- `test/` — unit tests plus a seeded property test over random graphs

## Test

```sh
npm test
```
