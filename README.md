# atomic-doc-edits

A small TypeScript library for applying a batch of text edits across
multiple versioned documents, with validate-then-publish atomicity and
guarded undo.

## Semantics

- **Batch edits, specified versions.** `applyBatch` takes one
  `DocumentEdit` per document, each carrying an `expectedVersion`. Every
  version is checked before anything is published.
- **Validate first, then atomically publish.** All versions and ranges
  are validated against the pre-batch state. If any check fails, a
  `BatchValidationError` (listing every issue) is thrown and *no*
  document is modified. The commit phase is a single synchronous pass.
- **Original coordinates, no overlap.** Edits for the same document are
  expressed in the coordinates of the document *before* the batch and
  must not overlap. Ranges are half-open `[start, end)`; adjacent edits
  (`end === next start`) are allowed, but two edits sharing a start
  offset are rejected even when one is empty, because their relative
  effect would depend on application order.
- **Guarded undo.** `undo(receipt)` restores the pre-batch text and
  version of every affected document, but only while each one is still
  at the version the batch produced. If any document has since moved
  on, an `UndoConflictError` is thrown and nothing is restored, so
  later edits are never overwritten. Undo is atomic across documents.

## Usage

```ts
import { DocumentStore } from 'atomic-doc-edits';

const store = new DocumentStore();
store.createDocument('a', 'hello world');
store.createDocument('b', 'foo');

const receipt = store.applyBatch([
  { docId: 'a', expectedVersion: 0, edits: [
    { range: { start: 6, end: 11 }, newText: 'there' },
    { range: { start: 0, end: 5 }, newText: 'hi' },   // original coordinates
  ] },
  { docId: 'b', expectedVersion: 0, edits: [
    { range: { start: 3, end: 3 }, newText: ' bar' }, // insertion
  ] },
]);
// a: "hi there" (v1), b: "foo bar" (v1)

store.undo(receipt); // back to v0 texts; throws UndoConflictError if
                     // either document has been edited since
```

## Errors

- `BatchValidationError` — `issues` lists every problem found
  (`unknown-document`, `duplicate-document`, `version-mismatch`,
  `invalid-range`, `range-out-of-bounds`, `overlapping-edits`).
- `UndoConflictError` — `conflicts` lists the documents whose current
  version no longer matches the receipt.

## Testing

Requires Node >= 22.6 (TypeScript type stripping; no build step).

```sh
npm test
```

Tests combine scenario specs (`test/store.test.ts`) with a snapshot-model
differential test (`test/model.test.ts`): an independent reference model
tracks full `(text, version)` snapshots, random batches and undos run
against both store and model, and the complete store snapshot is compared
after every step — including byte-identical state after every expected
failure.
