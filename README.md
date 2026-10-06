# anchor-doc

A TypeScript document library with boundary anchors, affinity-aware position
transformation, non-overlapping batch edits in original coordinates, and
undo/redo.

## Core semantics

- **Anchors live on boundaries.** An anchor sits on a gap between characters
  (position `0..text.length`), not on a character.
- **Insert affinity.** When text is inserted exactly at an anchor's boundary,
  `left` affinity keeps the anchor before the inserted text, `right` moves it
  after. Positions strictly before/after the edit are unchanged/shifted.
- **Deletion snap.** When a deletion (or the deleted part of a replacement)
  covers an anchor — any position in `[start, end]` — the anchor snaps to the
  deletion start.
- **Selections.** `transformSelection` transforms both ends. Defaults
  (`start: 'right'`, `end: 'left'`) keep text inserted exactly at a selection
  boundary outside the selection. An empty selection (cursor) is transformed
  as a single `right`-affinity position so it stays empty.
- **Batch edits.** `applyEdits` / `transformPositionThroughEdits` /
  `AnchorDocument.applyBatch` take edits in **original coordinates** that
  must not overlap. Adjacent ranges, stacked same-point insertions (applied
  in given order), and insertions exactly at a deletion boundary are allowed;
  an insertion strictly inside another edit's range is rejected.
- **Undo.** Every mutation records a snapshot; `undo()`/`redo()` restore both
  the text and all anchor positions. A batch is a single undo step.

## API

```ts
import {
  AnchorDocument,
  applyEdits,
  transformPosition,
  transformPositionThroughEdits,
  transformSelection,
  validateEdits,
} from 'anchor-doc';

const doc = new AnchorDocument('hello world');
doc.addAnchor('caret', 5, 'right');
doc.applyBatch([
  { start: 0, end: 0, text: '>> ' },   // insert at 0
  { start: 5, end: 11, text: 'there' },// replace "world" (original coords)
]);
doc.getAnchor('caret'); // { id: 'caret', pos: 8, affinity: 'right' }
doc.undo();             // text and anchors back to the original state
```

## Verification

The test suite cross-checks the library against an independent
**per-character marker model** (`test/model.ts`): the document is an array of
unique character markers and anchors are captured as neighbouring marker
identities, then re-located after each edit. 500 seeded fuzz rounds compare
texts, anchor positions and selection ends, specifically exercising adjacent
deletions, stacked same-point insertions and empty selections.

## Build & test

No network access is required: `scripts/compile.js` locates a TypeScript
compiler (`node_modules/typescript` first, then known local fallbacks).

```sh
npm test   # compile + run all tests
```
