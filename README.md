# fold-projection

Nested folding regions over a text document: a **visible-text projection**
(collapsed folds replaced by placeholders) with a **bidirectional offset
mapping** back to the original text, plus structural editing that keeps the
fold tree consistent.

Pure TypeScript, zero dependencies. Runs on Node.js ≥ 23.6 (native type
stripping); tests use the built-in `node:test` runner.

## Commands

```sh
npm test      # node --test (41 tests)
npm run demo  # runnable feature tour (examples/demo.ts)
```

## Model

- All public offsets are **UTF-16 code-unit offsets** (plain JS string
  indices). Fold and edit boundaries are snapped *outward* so a surrogate
  pair is never split (`src/utf16.ts`).
- A fold set is well-formed when every pair of regions is disjoint or
  properly nested. **Partially intersecting folds are rejected**
  (`validateFolds`, `FoldedDocument#addFold`) or **normalized** by merging
  each intersecting chain into its union (`normalizeFolds`,
  `setFolds(..., { normalize: true })`) — see `src/folds.ts`.
- `FoldedDocument` (`src/document.ts`) holds the original text plus a tree of
  `FoldNode { start, end, collapsed, children }`.

## Projection rules

- A collapsed fold renders as the placeholder (default `⋯`, configurable).
- Inner folds of a collapsed fold are hidden entirely — no nested
  placeholders. Expanding the outer fold reveals the inner ones, still
  collapsed (each node keeps its own `collapsed` state).
- Mapping: `toOriginalOffset` / `toVisibleOffset` for carets,
  `toOriginalRange` / `toVisibleRange` for selections. A selection touching a
  placeholder expands to the fold's **full original range**, so
  cross-placeholder selections always map to complete original spans.
- Line/character conversion (`positionAt` / `offsetAt` and the
  `visible*`/`original*` wrappers) treats LF, CRLF and lone CR as line
  breaks; CRLF counts as one break.

## Editing

`applyVisibleEdit(visibleStart, visibleEnd, inserted)` maps the visible range
to original coordinates, applies the edit to the original text, and adjusts
the fold tree:

- folds before the edit are untouched; folds after it shift by the delta;
- a fold containing the edit grows/shrinks (nesting state preserved);
- deleting a placeholder deletes the whole folded range and its folds;
- a fold only partially overlapped by a deletion is removed, while its
  surviving children keep their nesting and collapsed state.

## Layout

```
src/utf16.ts     UTF-16 safe offsets, line/character conversion (LF + CRLF)
src/folds.ts     region normalization / validation / fold-tree building
src/document.ts  FoldedDocument: projection, mapping, editing
test/            node:test suites (folds, projection, utf16, editing)
examples/demo.ts runnable tour: npm run demo
```
