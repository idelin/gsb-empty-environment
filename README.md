# Atomic Multi-Cursor Edit Transactions

TypeScript implementation of atomic multi-cursor edit transactions for a text
editor model. No dependencies; runs directly on Node.js >= 22.6 (type
stripping) and is tested with the built-in `node:test` runner.

## Model

- **One baseline coordinate system.** All edits staged in a transaction are
  expressed against the document as it was when the transaction began. They
  are applied at commit time in descending offset order, so a length-changing
  replacement later in the document can never shift the ranges of earlier
  ones.
- **Conflict rejection before writing.** Overlapping replacements, an
  insertion strictly inside another edit's range, or two insertions at the
  same offset raise `EditConflictError` during validation — before a single
  character of the document is touched.
- **Atomicity.** A transaction updates four state categories together: text,
  selections, annotations and the undo stack. If the callback throws or any
  commit step fails, all four are restored to their exact pre-transaction
  state and the error is rethrown.
- **UTF-16 offsets.** All public offsets are UTF-16 code unit offsets (like
  JS string indices and LSP). Any offset that would split a surrogate pair is
  rejected with `OffsetError`; `positionAt`/`offsetAt` convert between
  offsets and (line, character) positions for both LF and CRLF documents and
  never land inside a `\r\n` or a surrogate pair.
- **Tracked state.** Selections and annotations not explicitly staged are
  mapped through the committed edits so cursors and markers track the
  surrounding text. Each committed transaction pushes one undo record;
  `undo()` restores text, selections and annotations together.

## Usage

```ts
import { Editor } from './src/index.ts';

const editor = new Editor('const a = 1;\r\nconst b = 2;\r\n');

editor.transaction((tx) => {
  tx.replace(6, 7, 'alpha');   // baseline offsets
  tx.replace(20, 21, 'beta');  // still valid despite the growth above
  tx.setSelections([{ anchor: 6, head: 11 }]);
  tx.addAnnotation({ id: 'todo', range: { start: 0, end: 5 } });
});                            // commits all four state categories atomically

editor.undo();                 // restores text + selections + annotations
```

## Scripts

- `npm test` — run the `node:test` suite (29 tests)
- `npm run demo` — run `examples/demo.ts` (3-cursor CRLF transaction,
  conflict rejection, rollback on callback failure, undo)

## Files

- `src/offsets.ts` — UTF-16 offset validation, surrogate-pair guards,
  LF/CRLF-aware position conversion
- `src/transaction.ts` — edit validation, conflict detection, ordered
  application, inverse-edit computation, offset/range mapping
- `src/editor.ts` — `Editor` + `Transaction`: staging, atomic commit with
  snapshot rollback, selections, annotations, undo stack
- `test/editor.test.ts` — `node:test` suite
- `examples/demo.ts` — runnable walkthrough
