# ts-lex

Streaming, checkpointable and incremental lexer for TypeScript-like syntax.

- **Tokens**: `identifier`, `number`, `string` (double-quoted, `\` escapes),
  `lineComment` (`// …`), `blockComment` (`/* … */`, nestable), `whitespace`,
  `punct`. Comment openers inside strings (and quotes inside comments) are
  inert. Tokens tile the input and carry absolute UTF-16 `start`/`end` offsets.
- **Streaming**: `feed(chunk)` accepts chunks split at any UTF-16 boundary;
  the token stream is identical to one-shot lexing. `end()` reports
  unterminated constructs with their absolute start offsets (for block
  comments, the full stack of unclosed nested `/*` positions).
- **Checkpoints**: `checkpoint()` snapshots the unfinished string, the nested
  comment stack, the pending token and the source offset. `Lexer.restore(cp)`
  resumes a scan that is indistinguishable from a continuous one.
- **Incremental**: `IncrementalLexer.applyEdit(start, end, inserted)` takes
  coordinates in the *previous* document version. Rescanning starts at the
  token boundary at or before the edit (never a fixed number of lines back)
  and stops as soon as a freshly lexed token past the edited region matches
  the previous scan — at a token boundary the lexical state is always
  `normal`, so boundary equality implies state equality.

## Usage

```js
const { Lexer, IncrementalLexer } = require('ts-lex');

const lexer = new Lexer();
lexer.feed('let s = "he');
lexer.feed('llo" /* c /* n */ d */');
console.log(lexer.end());        // [] — or unterminated-* diagnostics
console.log(lexer.tokens);       // absolute UTF-16 ranges

const inc = new IncrementalLexer('/* a */ x /* b */ y');
inc.applyEdit(0, 2, '');         // old-version coordinates; tail becomes code
```

## Tests

```
npm test
```

- enumerates **every** chunk split of short inputs and compares against
  one-shot lexing, plus randomized chunkings of long mixed inputs;
- pauses/resumes at every offset (and every pair of offsets) of tricky
  inputs and compares against continuous scanning;
- applies exhaustive single-char insert/delete/replace edits, sequential
  random edits in old-version coordinates, and char-by-char edits that
  create/destroy comment openers, verifying far-end comment-state
  propagation against fresh lexing after every step.
