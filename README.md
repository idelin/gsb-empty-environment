# atomic-doc-edits

A small TypeScript library for committing a batch of text edits across
multiple documents **atomically**, with optimistic version checks and safe
undo.

## Guarantees

- **Validate first, publish once.** `applyBatch` checks every change —
  document existence, `expectedVersion`, edit ranges, non-overlap — and
  collects *all* errors before touching anything. If any check fails, a
  `BatchValidationError` is thrown and **no document is modified**.
- **Original coordinates, no overlap.** Edits targeting the same document
  are offsets into the document's text *before* the batch. They may be given
  in any order but must not overlap (two insertions at the same offset are
  also rejected as ambiguous). A document may appear only once per batch.
- **Atomic commit.** After validation succeeds, all documents are updated
  and their versions bumped together, then a single change event is
  published to listeners.
- **Safe undo.** `undo(receipt)` requires every affected document to still
  be at the version the batch produced. If any document received later
  edits, the whole undo is rejected and nothing is reverted, so newer work
  is never silently overwritten.

## Usage

```ts
import { Workspace } from 'atomic-doc-edits';

const ws = new Workspace();
ws.openDocument('a.ts', 'const alpha = 1;\n');
ws.openDocument('b.ts', 'export const beta = 2;\n');

const receipt = ws.applyBatch([
  { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 6, end: 11, newText: 'ALPHA' }] },
  { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: '// header\n' }] },
]);

ws.undo(receipt); // restores both documents to version 1
```

## Verification

Tests run on the Node.js built-in test runner (Node ≥ 22.6 runs TypeScript
via type stripping; no build step or dependencies needed):

```sh
npm test
```

The suite includes a **snapshot model** test: a seeded random sequence of
batches, undos, and deliberately invalid batches is applied both to the
`Workspace` and to an independent plain-object model, and the workspace
`snapshot()` is deep-compared against the model after every step.
