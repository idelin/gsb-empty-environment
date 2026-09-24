# bracket-match-lang

Bracket matching and mismatch diagnostics for a small language with strings
and comments, written in TypeScript and tested with the Node.js built-in
test runner (`node:test`). No runtime dependencies.

## Language

- Brackets: `()` `[]` `{}`
- Strings: `"..."` and `'...'` with backslash escapes; they do not span
  lines (an unterminated string is a diagnostic)
- Comments: `//` line comments and `/* ... */` block comments
- Brackets inside strings and comments never participate in matching

## Design

- **UTF-16 offsets everywhere.** All public offsets are UTF-16 code-unit
  offsets (JavaScript string indices). Edits that would split a surrogate
  pair are rejected with a `RangeError`; `positionAt`/`offsetAt` never land
  inside a pair. LF and CRLF line endings are both handled.
- **Incremental lexer** (`src/lexer.ts`). Keeps the token stream plus the
  lex state at every token start. A local edit re-lexes only from the
  touched token until the stream resynchronizes with the old one, so typing
  in the middle of a file is O(affected region), while `/*` at the top
  correctly re-lexes to EOF.
- **Iterative matcher** (`src/brackets.ts`). Pairing, diagnostics, depth
  and parent links are computed with explicit stacks — no recursion
  anywhere, so nesting depths of 10 000 (and 100 000) are safe.
- **Queries.** `matchAt(offset)` maps a bracket to its counterpart;
  `enclosingPair(offset)` binary-searches the pair list and walks parent
  links to return the *minimal* enclosing pair.
- **Diagnostics.** `unmatched-close`, `mismatched-close` (with recovery:
  the stack is kept, so later brackets still match), `unclosed-open`,
  `unterminated-string`, `unterminated-block-comment`. Because matching is
  recomputed from the corrected token stream after every edit, deleting a
  middle opener immediately updates all downstream mismatches.

## API

```ts
import { LanguageService } from './src/index.ts';

const svc = new LanguageService('fn main() { return (1 + [2]); }');
svc.applyEdit(24, 24, '3');            // local edit, UTF-16 offsets
svc.getPairs();                        // matched pairs, sorted by open offset
svc.getDiagnostics();                  // mismatch / unterminated diagnostics
svc.matchAt(11);                       // matching bracket offset, or null
svc.enclosingPair(30);                 // minimal enclosing pair, or null
svc.positionAt(11);                    // { line, character } (LF & CRLF)
svc.offsetAt({ line: 0, character: 11 });
```

## Requirements

Node.js >= 22.6 (uses native TypeScript type stripping; works out of the
box on >= 23.6). No packages need to be installed.

## Run

```sh
npm test     # node --test with the built-in runner
npm run demo # end-to-end sample in examples/demo.ts
```

Optional static type check (requires `npm i -D typescript @types/node`):

```sh
npm run typecheck
```

## Sample run (`npm run demo`)

```
=== 1. Parse a small program (strings/comments hold no brackets) ===
fn main() {
  let s = "brackets (inside) strings [don't] count";
  // a comment with ( parens [ galore
  /* block { comment } */
  let x = (1 + [2, 3]);
}
  pairs (open -> close, depth):
    ( @7  ->  ) @8  (depth 1)
    { @10  ->  } @153  (depth 1)
    ( @139  ->  ) @150  (depth 2)
    [ @144  ->  ] @149  (depth 3)
  diagnostics: (none)

=== 2. Minimal enclosing pair query ===
(a {b} [c] d)
  offset  4 ("b"): enclosed by {@3 ... }@5
  offset  8 ("c"): enclosed by [@7 ... ]@9
  offset 11 ("d"): enclosed by (@0 ... )@12
  offset  6 (" "): enclosed by (@0 ... )@12
  offset  0 ("("): no enclosing pair

=== 3. Insert ")" inside a string: outer structure unchanged ===
  before: f("x")  pairs=[[1,5]]
  after:  f("x)")  pairs=[[1,6]]
  diagnostics: 0 (inserted ")" matches nothing: matchAt(4)=null)

=== 4. Delete a middle "(": downstream mismatches update ===
  before: ( [ ) ]
  diagnostics:
    [unclosed-open] offset 0 (line 1, col 1): Opening bracket '(' is never closed
    [mismatched-close] offset 4 (line 1, col 5): Expected ']' to close '[' at offset 2, but found ')'
  after:  ( ) ]
  diagnostics:
    [unmatched-close] offset 4 (line 1, col 5): Closing bracket ']' has no matching opener

=== 5. CRLF line endings ===
  "(" at offset 10 matches ")" at offset 12
  position of ")": {"line":1,"character":4}

=== 6. UTF-16 offsets and surrogate pairs ===
  text "😀(x)" has length 5 UTF-16 code units
  pair: [[2,4]]
  edit splitting the emoji rejected: Edit start 1 splits a surrogate pair

=== 7. Depth 10000 (iterative, no recursion) ===
  pairs: 10000, diagnostics: 0
  innermost pair around the middle: (9999, 10001) at depth 10000
  matchAt(0) = 20000  (enclosing-pair query took 0.01 ms)
```

## Test results (`npm test`)

36 tests, all passing (Node v23.9.0):

```
ℹ tests 36
ℹ pass 36
ℹ fail 0
```

- `test/document.test.ts` — UTF-16 offsets, surrogate-pair protection,
  LF/CRLF line index, edit validation
- `test/lexer.test.ts` — strings, escapes, line/block comments,
  unterminated constructs, LF and CRLF
- `test/brackets.test.ts` — pairing, mismatch diagnostics, `matchAt` /
  `pairAt` / `enclosingPair`, emoji offsets
- `test/edit.test.ts` — local edits: `)` inserted in a string, deleting a
  middle opener, `/*` toggling, CRLF edits, plus a randomized
  incremental-lexer vs full-re-lex consistency check (400 edits)
- `test/depth.test.ts` — 10 000 and 100 000 nesting depth, iterative
  matching, edits at depth

## Layout

```
src/textutil.ts   surrogate-pair helpers
src/document.ts   text buffer, UTF-16 offsets, LF/CRLF line index
src/lexer.ts      pure token lexer + incremental re-lexer
src/brackets.ts   iterative matcher, diagnostics, enclosing-pair queries
src/service.ts    LanguageService facade
examples/demo.ts  runnable end-to-end sample
test/             node:test suites
```
