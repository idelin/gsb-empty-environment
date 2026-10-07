# outline-dnd

TypeScript library for outline-tree drag-and-drop. Moves multiple,
non-adjacent selected nodes in one atomic operation.

## Semantics

- **Effective selection**: when both an ancestor and its descendant are
  selected, the descendant is ignored (it travels with the ancestor).
- **Target coordinates**: the drop target index is expressed in
  *pre-removal* coordinates of the target parent's children; the library
  adjusts for the moved nodes that were removed before that index.
- **Order**: moved nodes are inserted in source document (pre-order) order,
  regardless of the order the ids were passed in.
- **Rejection**: moving into a selected node or any descendant of a
  selected node fails with target-inside-selection; unknown ids and
  out-of-range indexes also fail. Failures never mutate the document or
  the undo history.
- **Undo/redo**: every successful move pushes an inverse record; undo()
  restores each moved node to its exact original parent and index.

## API

    import { OutlineDocument, createNode } from "outline-dnd";

    const doc = new OutlineDocument([
      createNode("A", "Alpha", [createNode("a1"), createNode("a2")]),
      createNode("B"),
      createNode("C"),
    ]);

    doc.move(["B", "a1"], { parentId: null, index: 3 }); // { ok: true }
    doc.undo();

Helpers for verification: flatten() (pre-order entries with depth and
parentId), parentOf(id), isAncestor(ancestorId, id).

## Tests

Requires Node.js >= 22.6 (native TypeScript type stripping):

    npm test
