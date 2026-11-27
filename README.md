# sql-jsonpath-ts

Zero-dependency TypeScript implementation of a SQL/JSON Path (SQL:2016
`jsonpath`) engine with PostgreSQL-compatible `lax`/`strict` semantics and a
GIN `jsonb_path_ops`-style index pushdown planner.

The library ships three layers that can be used independently:

1. **Parser** — text `jsonpath` to a typed AST.
2. **Evaluator** — sequence-based path evaluation with Kleene three-valued
   filter logic.
3. **Pushdown planner + in-memory GIN index** — sound pre-filter plans that
   guarantee zero false negatives against full evaluation.

## Quick start

```ts
import {
  parseJsonPath,
  queryJsonPath,
  matchJsonPath,
  extractIndexPlan,
  GinPathIndex,
} from './src/index.ts';

const doc = { users: [{ name: 'alice', age: 30 }, { name: 'bob' }] };

queryJsonPath(doc, 'lax $.users[*].name');
// ['alice', 'bob']

matchJsonPath(doc, 'strict $.users[*] ? (@.age >= 18)');
// true

const index = new GinPathIndex();
index.addDocument(0, doc);
const candidates = index.preFilter(parseJsonPath('lax $.users[*] ? (@.age == 30)'));
// [0] — every document the evaluator would match is guaranteed to be included
```

Build the emitted ESM package with `npm run build`; tests run against the
compiled output via `npm test` and type checking is `npm run typecheck`.

## Supported grammar

- Mode declaration: `lax` (default) / `strict`.
- Roots and current item: `$`, `@`, named variables `$name` (supplied via
  `EvalOptions.vars`).
- Member access: `.key`, `"quoted key"`, wildcard `.*`.
- Array access: `[*]`, comma lists `[i, j]`, slices `[start to end]`,
  negative literals `[-1]`, `last` and `last - 1` arithmetic bounds.
- Recursive descendant: `.**` (depth-first, item first).
- Filters `? ( ... )` with `&&`, `||`, `!`, comparisons `==`, `!=`, `<`,
  `<=`, `>`, `>=`, `exists(...)`, arithmetic `+ - * / %`, and
  `like_regex "pattern" flag "flags"` (`i`, `s`, `m`, `x`).
- Methods: `.type()`, `.size()`, `.double()`.

## Dual-mode semantics

### Lax

- Every accessor step first recursively flattens all nested arrays, so
  `$.a.x` on `{ a: [[{ x: 1 }], { x: 2 }] }` returns `[1, 2]`.
- Missing members, non-object member access, non-array subscripts and
  out-of-bounds integer indices suppress the structural error and produce an
  empty sequence instead of throwing.
- Non-array scalars are treated as one-element arrays inside subscripts.
- Array slices always clamp their bounds (in both modes) and never raise.

### Strict

- Arrays are never auto-unwrapped. Member access on an array, a missing key,
  a subscript on a non-array, or an out-of-bounds index throws a structured
  `JsonPathError` carrying a stable symbolic `code` and source `position`.

### Filters and three-valued logic

Any error raised while evaluating a `? ( ... )` predicate is caught and
mapped to `UNKNOWN`; the surrounding path keeps evaluating. Kleene truth
tables apply:

| A | B | A && B | A \|\| B | !A |
| --- | --- | --- | --- | --- |
| TRUE | UNKNOWN | UNKNOWN | TRUE | FALSE |
| FALSE | UNKNOWN | FALSE | UNKNOWN | TRUE |
| UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN |

Crucially `!(UNKNOWN) === UNKNOWN`, never `TRUE`.

Comparisons are existential over sequences: a pair of items satisfying the
operator yields `TRUE`; if every pair raises or is type-incompatible the
result is `UNKNOWN`; an empty sequence on either side is defined `FALSE`
(so its negation is `TRUE`, not `UNKNOWN`). Ordering is only defined between
two numbers or two strings; other combinations give `UNKNOWN`.

## Index pushdown

`extractIndexPlan(ast)` returns an `IndexPlan`:

- `fullScan: true` — no safe constraint; the index must return every document.
- Otherwise a DNF of `Conjunction` groups, each combining:
  - `exact` facts (member existence or scalar leaf on a fixed key path),
  - `suffixKeyPaths` / `anchoredSuffixes` for wildcard/recursive paths,
  - `anchoredLeaves` for value matches behind a wildcard barrier.

Soundness rules deliberately err on the side of returning more candidates:

- Outer `!`, `!=`, `like_regex`, arithmetic-only predicates, or named
  variables force `FULL_SCAN`.
- An `||` with any unselective branch forces `FULL_SCAN`; safe branches are
  unioned as separate DNF groups.
- `&&` intersects groups.
- `.*` / `.**` destroy positional certainty, so subsequent requirements are
  anchored to the subtree at the barrier and matched as suffixes.
- Array positions never appear in facts: arrays are transparent, matching
  GIN `jsonb_path_ops` hashing.

The property tests in `tests/pushdown.test.ts` and
`tests/adversarial.test.ts` build random nested document corpora and assert,
for hundreds of generated expressions in both modes, that every evaluator
match survives the index pre-filter.

## Error model

`JsonPathError` exposes:

- `code` — stable symbolic identifier (e.g.
  `JSON_PATH_INDEX_OUT_OF_BOUNDS`, `JSON_PATH_MEMBER_NOT_FOUND`,
  `JSON_PATH_DIVISION_BY_ZERO`),
- `sqlstate` — best-effort SQLSTATE string (default `22023`),
- `position` — byte offset into the source expression when available.

Structural error codes are exported through `isStructuralError` and are the
only errors lax mode suppresses; arithmetic, regex and type errors still
surface outside a filter.

## Project layout

```
src/
  ast.ts         AST node types
  errors.ts      structured JsonPathError + structural-code table
  lexer.ts       tokenizer
  parser.ts      recursive-descent parser
  evaluator.ts   dual-mode evaluation + three-valued predicates
  index-plan.ts  sound pushdown constraint extraction
  gin-index.ts   in-memory inverted index and plan evaluation
  index.ts       public entry point
tests/           acceptance, pushdown, adversarial and soundness suites
```
