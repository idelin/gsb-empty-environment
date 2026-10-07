# frozen-replace

A TypeScript replace library built on frozen snapshots, simultaneous edit
application, bidirectional position mapping, and multi-document transactions.

## Replace semantics

- **Frozen matching**: `replaceAll(text, pattern, replacement)` first finds
  every non-overlapping match against the frozen old text (leftmost,
  `lastIndex`-style scanning), then applies all replacements simultaneously.
  Matches that the replacement text itself would create are not processed in
  this round, and replacements never see each other's output.
- **Zero-width matches**: an empty match consumes its boundary — the scan
  resumes one char later, so each boundary matches at most once.
- **Replacement templates** (JavaScript `String.replace` semantics):
  `$$` (escaped literal `$`), `$&` (whole match), `` $` `` / `$'` (frozen
  before/after context), `$1`..`$99` (capture groups; non-participating
  groups expand to empty), `$<name>` (named groups). Function replacements
  are also supported.

## Position mapping

Every replace/apply returns a `PositionMap` between the old and new text:

- `oldToNew(offset, bias)` / `newToOld(offset, bias)` — `bias: 'before' | 'after'`
  resolves offsets inside replaced regions and at zero-width insertions.
- `oldRangeToNew(range)` / `newRangeToOld(range)` — half-open range mapping.

Correctness is checked against a naive concatenation model: the new text must
equal splicing unchanged slices and replacements together, replacements must
land at the naive offsets, kept characters must survive at their mapped
offsets, and boundary offsets must round-trip.

## Documents and transactions

`TextDocument` carries a `version` bumped on every mutation. A `Transaction`
bundles edits for several documents, each pinned to an `expectVersion`:

- `commit()` applies all documents' edits simultaneously, but only if every
  document is still at its expected version; any concurrent modification
  rejects the entire batch atomically (no document is touched).
- On success it returns per-document forward edits, inverse edits, and range
  mappings, plus `undo()` which restores every document's text and version.

## Develop

```sh
npm run build   # type-check + emit to dist/
npm test        # build, then run the test suite
```
