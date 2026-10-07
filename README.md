# offline-filter

Offline row filtering with three-valued logic expressions: `AND`, `OR`,
`NOT`, comparisons (`eq ne lt le gt ge`), and `IS NULL` / `IS NOT NULL`.

## Semantics

- Every predicate evaluates to a three-valued result: `T` (true), `F`
  (false), or `U` (unknown), combined with the Kleene truth tables.
- A missing field and an explicit `null` are both *absent*. Any comparison
  involving an absent operand is unknown — never an error, never true.
- `IS NULL` is true exactly when the operand is absent; `IS NOT NULL` is its
  inverse. Falsy values (`0`, `''`, `false`) are **not** null.
- `filterRows` selects exactly the rows whose predicate evaluates to `T`;
  unknown and false are both dropped.

## Short-circuiting and error priority

`evaluate` (src/evaluate.ts) short-circuits: `AND` skips its right branch
when the left is `F`, `OR` skips it when the left is `T`. A skipped branch is
never evaluated — its fields are not read and its type errors never happen.
Errors in branches that *are* evaluated propagate immediately, preserving
left-to-right error priority, and carry the position of the failing
comparison (`EvalError.path`, e.g. `['right', 'left']` rendered as
`$.right.left`).

Type errors: comparing operands of different runtime types, and ordering
(`lt le gt ge`) booleans. Absent operands yield unknown instead of an error.

## Reference interpreter

src/reference.ts is a non-short-circuiting truth-table interpreter used to
verify the evaluator. `evaluateAll` values every node with the Kleene tables
and collects type errors per node; `reachableErrors` projects the errors a
short-circuiting run must observe, in evaluation order; `expectedOutcome`
combines both into the expected value-or-error for any (expression, row)
pair. test/reference.test.ts cross-checks the evaluator against it over an
enumerated space of ~40k expression/row combinations, asserting both the
selected rows and the exact positions of type errors.

## Usage

```ts
import { and, cmp, field, filterRows, isNull, lit, or } from 'offline-filter';

const pred = and(
  cmp('gt', field('age'), lit(18)),
  or(cmp('eq', field('tag'), lit('x')), isNull(field('tag'))),
);
const selected = filterRows(rows, pred);
```

## Development

```sh
npm install   # devDependency: typescript
npm run build # compile src -> dist
npm test      # compile src+test -> build, run node:test suites
```
