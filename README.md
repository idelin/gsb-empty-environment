# incremental-highlighter

A TypeScript incremental syntax-highlighting library for a JS/TS-like language.
It handles constructs that span lines — template literals (with `${ }`
interpolation and nesting), backslash-continued strings, and block comments —
by storing lexical state per line.

## Design

- **Per-line state.** Every line stores its *input* lexical state, its tokens,
  and its *output* lexical state (`src/highlighter.ts`). The state is a small
  stack of open constructs (`src/state.ts`): block comment, template-literal
  text, `${ }` interpolation (with brace depth), or a backslash-continued
  string.
- **Incremental recomputation.** After an edit, lines are retokenized from the
  first dirty line forward. Recomputation stops only at a line whose
  recomputed input state **and** tokens both match what is already stored —
  since tokenization is deterministic, everything downstream is then provably
  unchanged. There is **no fixed look-ahead window**: deleting a backtick or a
  `*/` propagates as far as necessary, up to end of file.
- **UTF-16 ranges.** All offsets (edit ranges, token ranges) are UTF-16
  code-unit offsets, i.e. plain JavaScript string indices. An astral character
  such as an emoji counts as 2.
- **Full-highlight parity.** Tests cross-check every incremental result
  against a from-scratch full highlight (token stream + per-line input
  states), including delete-quote/restore round trips and a seeded fuzz run.

## API

```ts
import { IncrementalHighlighter } from 'incremental-highlighter';

const h = new IncrementalHighlighter('const s = `a\nb`;\n');
h.applyEdit({ start: 11, end: 12, text: '' }); // delete the backtick
h.getTokens();       // Token[] with absolute UTF-16 ranges
h.getLineTokens(0);  // line-relative tokens
h.getLineState(1);   // input lexical state of line 1
h.lastRetokenized;   // lines retokenized by the last edit (instrumentation)
```

## Build & test

```sh
npm install   # needs typescript (devDependency)
npm test      # tsc + node dist/test/run.js
```
