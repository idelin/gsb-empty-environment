# ast-bind

A TypeScript query-AST library that binds values by **parameter node
identity**, renders SQL with positional placeholders, and verifies itself
by rendering then re-parsing.

## Binding rules

- Values enter a query only through `Param` nodes (`param(value)`).
- Binding is keyed by node identity (object reference), never by value
  equality: two nodes holding equal values get **distinct** placeholders;
  the same node referenced from several places gets **one** placeholder.
- Placeholders `$1..$n` are numbered in a deterministic traversal order:
  pre-order depth-first over columns → FROM → WHERE → ORDER BY → LIMIT →
  OFFSET; nested subqueries are numbered at the position where they appear.
- `raw(text)` fragments are emitted verbatim and can never carry values —
  there is no interpolation API, and the renderer rejects fragments
  containing placeholder syntax (`$n` or `?`).
- Identifiers are always double-quoted, embedded quotes doubled
  (`we"ird` → `"we""ird"`).
- Empty lists: `x IN ()` → `(1 = 0)`, `x NOT IN ()` → `(1 = 1)`;
  `AND ()` → `(1 = 1)`, `OR ()` → `(1 = 0)`.

## Verification (render → reparse)

`verifyRoundTrip(query)` renders the AST, re-parses the SQL with the
built-in parser (`src/parse.ts`), and walks both sides in the same
deterministic order to check:

1. **Binding correspondence** — every `Param` occurrence maps to the
   placeholder number found at the same traversal position, and
   `bindings[$n - 1]` is that node's value.
2. **Empty lists** — constant predicates contain no placeholders and bind
   nothing (dropped item expressions are excluded from both walks).
3. **Identifier escaping** — identifier paths round-trip through
   quoting/unquoting unchanged.

Note: the re-parser covers the rendered dialect; `raw()` fragments join
verification only if they stay within the parseable subset (e.g.
`COUNT(*)`) and introduce no identifiers.

## Example

```ts
import { select, fromTable, ident, field, and, inSubquery, eq, gt, param, render } from 'ast-bind';

const tenant = param('t1');
const inner = select({
  columns: [field('orders.user_id')],
  from: [ident('orders')],
  where: eq(field('orders.tenant'), tenant),
});
const q = select({
  columns: [field('u.id')],
  from: [fromTable(ident('users'), 'u')],
  where: and(inSubquery(field('u.id'), inner), eq(field('u.tenant'), tenant)),
});

render(q);
// sql:      SELECT "u"."id" FROM "users" AS "u"
//           WHERE (("u"."id" IN (SELECT "orders"."user_id" FROM "orders"
//                  WHERE ("orders"."tenant" = $1))) AND ("u"."tenant" = $1))
// bindings: ['t1']   — one Param node, one placeholder, used twice
```

## Layout

- `src/ast.ts` — node types and builders
- `src/render.ts` — renderer: identity binding, numbering, escaping
- `src/parse.ts` — tokenizer + recursive-descent parser for the rendered dialect
- `src/verify.ts` — deterministic-order collectors and `verifyRoundTrip`
- `test/` — node:test suites for binding, subqueries, empty lists,
  identifier escaping, raw fragments

## Running the tests

Requires Node ≥ 22.6 (type stripping); on 23.6+ no flag is needed:

```sh
npm test          # node --experimental-strip-types --test test/*.test.ts
```
