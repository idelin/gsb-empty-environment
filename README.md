# expr-edit

A configuration-expression editing tool. It parses expressions that carry
line comments, lets you replace a chosen AST node, and prints text that
parses again to the same tree.

## Language

Identifiers, decimal integers, function calls, parentheses, unary minus,
and the binary operators `+ - * /`. Binary operators are left-associative;
`*` and `/` bind tighter than `+` and `-`. Line comments start with `#`.
No parser library is used — the lexer and recursive-descent parser are
hand-written in `src/lexer.js` and `src/parser.js`.

## Comment attachment rules

- A comment on the same line as a token attaches as **trailing** trivia of
  that line's last token.
- A comment on its own line attaches as **leading** trivia of the next
  token.
- Comments with no following token stay as leading trivia of the EOF
  token, i.e. they are preserved at the file tail.

Every token keeps its source span (`start`/`end` offsets), and every AST
node records its first/last token indices, so a node maps back to its
exact source range.

## API

- `parse(src)` → `{ ast, tokens, src }`; throws `ParseError` with `pos`
  (earliest offset where parsing cannot continue) and `expected` (set of
  expected constructs).
- `print(parsed)` / `format(src)` → reprints with normalized whitespace.
  Never drops a comment, never changes the AST, never merges tokens;
  `format` is idempotent.
- `replaceNode(parsed, target, replacement)` → new source string with the
  target node's span swapped for the canonical rendering of `replacement`.
  Comments outside the node (leading trivia of its first token, trailing
  trivia of its last token) are preserved.
- `findNode(ast, pred)` → first matching node (depth-first).
- `render(ast)` → canonical text that re-parses to a structurally equal
  AST (parentheses added only where the grammar requires them).
- `evaluate(ast, env)` → independent integer interpreter (truncating
  division, throws on division by zero) for semantic cross-checks.

## Tests

```
npm test    # node test/run-tests.js
```

Covers `(a-b)-c` vs `a-(b-c)`, chained unary minus, empty argument lists,
multi-line calls, tail comments, `parse(print(ast))` structural
equivalence, formatting idempotence, comment-order preservation for
untouched nodes, error positions/expected sets, and interpreter
cross-checks on division-by-zero-free samples.
