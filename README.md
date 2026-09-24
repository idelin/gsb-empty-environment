# bracket-match-lang

Bracket pairing and mismatch diagnostics for a small language with strings
and comments, written in TypeScript and tested with the Node.js built-in
test runner (`node:test`). No runtime dependencies.

## Language rules

- Brackets: `( ) [ ] { }`
- Line comments `// ...`, block comments `/* ... */` (non-nesting)
- Strings `"..."` / `'...'` with `\` escaping the next character
- Brackets inside strings or comments never participate in matching
- Unterminated strings/comments are diagnosed; their contents (to end of
  line / end of file) are treated as string/comment

## Guarantees

- **UTF-16 offsets**: all public offsets are UTF-16 code-unit offsets
  (JavaScript string indices). `applyEdit`, `enclosingPair` and `positionAt`
  throw `RangeError` on offsets that would split a surrogate pair;
  `offsetAt` throws if the resolved offset would split one.
- **Line endings**: LF, CRLF and lone CR are all handled by
  `positionAt` / `offsetAt`.
- **Local edits**: `applyEdit(start, end, replacement)` splices the text and
  re-analyzes, so cross-block structure, nesting and diagnostics (including
  mismatches after the edit point) stay correct.
- **No recursion**: lexing, matching, pair-tree construction and enclosing
  pair queries all use explicit stacks/loops — nesting depths of 10 000+
  cannot overflow the call stack.
- **Mismatch recovery**: when a closing bracket does not match the innermost
  opener but a matching opener exists further out, the intervening openers
  are reported as `unclosed-opening` and the pair is matched; otherwise the
  closer is reported as `unmatched-closing`. Later brackets keep matching.

## API

```ts
import { BracketAnalyzer } from './src/index.ts';

const a = new BracketAnalyzer('fn main() { go(); }');
a.getPairs();                 // matched pairs, sorted by opening offset
a.getDiagnostics();           // mismatches, sorted by start offset
a.enclosingPair(offset);      // minimal pair containing a UTF-16 offset, or null
a.positionAt(offset);         // offset -> { line, character } (0-based, UTF-16)
a.offsetAt({ line, character }); // position -> offset (clamped to line content)
a.applyEdit(start, end, '');  // local edit, then everything is recomputed
```

Diagnostics use codes: `unmatched-closing`, `unclosed-opening`,
`unterminated-string`, `unterminated-comment`.

## Run

Requires Node.js >= 22.6 (uses native TypeScript type stripping; verified on
Node 23.9).

```sh
npm test        # node --test  (28 tests)
npm run sample  # node examples/sample.ts
```

For static type-checking, install dev dependencies when network is available
(`npm i -D typescript @types/node`) and run `npx tsc --noEmit`; the code uses
erasable-syntax-only TypeScript (`tsconfig.json` sets `erasableSyntaxOnly`).
