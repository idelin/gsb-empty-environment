# incremental-lexer

Incremental line-state lexer for a tiny language (keywords, numbers, strings
with `\"` escapes, `//` line comments, `/* ... */` block comments), written in
TypeScript with zero dependencies. Runs on Node.js >= 23 (native type
stripping) — no build step.

## Design

- The document is split into lines (LF, CRLF and lone CR are all supported).
- Each line caches `{ entry, exit }` lexer state plus its tokens, with token
  offsets stored **relative to the line start** so that edits shifting a
  line's absolute position never stale the cache.
- `applyEdit(start, end, inserted)` invalidates the touched lines, re-lexes
  from the first touched line, and propagates forward **only until a cached
  line's entry state matches again** — everything downstream is provably
  unchanged. It returns `{ invalidatedLines }`, the 0-based lines that were
  actually re-lexed and may need re-coloring.
- All public offsets are **UTF-16 code units** (plain JS string indices).
  Edit boundaries that would split a surrogate pair are rejected with a
  `RangeError`; the scanner itself is astral-aware, so a token never cuts a
  surrogate pair in half.

## Consequences (covered by tests)

- Deleting an early `*/` recolors every following line up to the next `*/`
  (or EOF); re-inserting a terminator restores the old colors.
- Typing one character inside an identifier invalidates only its own line.
- `\"` never terminates a string; an unterminated string ends at EOL and
  leaks no state into the next line.

## Usage

```ts
import { IncrementalLexer } from './src/lexer.ts';

const lexer = new IncrementalLexer('let a = "x"\n/* c\nd */\n');
console.log(lexer.getTokens());           // [{ type, start, end }, ...] UTF-16 offsets
const { invalidatedLines } = lexer.applyEdit(14, 16, ''); // delete "*/"
```

## Run

```sh
npm run demo   # colored sample session
npm test       # node:test suite (19 tests)
```
