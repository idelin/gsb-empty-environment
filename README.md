# terminal-layout

Monospaced terminal-style layout engine in TypeScript over an editable piece
tree. Runs on Node.js (>= 22.6) with native type stripping — no build step.

- Tab expansion to the next tab stop (`tabWidth`, default 4)
- Wide clusters take 2 columns; zero-width combining marks take none
- Grapheme cluster boundaries and widths come from the caller's `Measurer`;
  clusters (incl. combining sequences) are never split
- Soft wrap at `wrapColumn`, per-line lazy measurement with an incremental
  cache: editing one line never re-measures unrelated lines
- Logical UTF-16 offsets <-> visual (line, row, column) conversion; ambiguous
  positions return one candidate plus a directional affinity, not a fake
  bijection. Surrogate pairs are never split. LF and CRLF terminators.
- `snapshot()` freezes the current layout for old-layout hit testing

## Commands

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## Layout

- `src/measurer.ts` — `Measurer` contract + default `Intl.Segmenter` measurer
- `src/pieceTree.ts` — editable piece table with incremental line index
- `src/layout.ts` — `LayoutEngine` / `LayoutSnapshot`
