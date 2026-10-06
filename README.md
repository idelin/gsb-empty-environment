# unit-checker

Dimensional analysis checker for formulas, written in TypeScript.

## Model

A unit is an **integer exponent vector** over base dimensions (`L`, `M`, `T`, …):

| Operation | Vector algebra |
|-----------|----------------|
| `a * b`   | `u(a) + u(b)`  |
| `a / b`   | `u(a) - u(b)`  |
| `a ^ n`   | `n · u(a)` (integer `n`, negative allowed) |
| `a + b` / `a - b` | requires `u(a) = u(b)`, result is that unit |

Numeric literals — **including `0`** — are dimensionless (`1`). Zero is *not*
polymorphic: `v + 0` fails with `UNIT_MISMATCH` instead of silently giving `0`
the unit of `v`. Dimensionless results arise naturally as the zero vector
(e.g. `d / d`).

## Usage

```sh
node --experimental-strip-types src/cli.ts "v * t + d"   # or run with no args for a demo suite
node --experimental-strip-types --test test/*.test.ts    # tests
```

(Requires Node ≥ 23.6 for built-in TypeScript type stripping, or compile with `tsc`.)

## API

- `parse(source)` → `Expr` — recursive-descent parser for
  `expr := term (('+'|'-') term)*`, `term := power (('*'|'/') power)*`,
  `power := atom ('^' signed-int)?`, `atom := number | ident | '(' expr ')'`.
- `check(expr, env)` → `{ tree, unit, minimalErrors }` where `env` maps
  variable names to exponent vectors.
- `formatTree(node)` — renders the derivation tree, one judgement per line,
  failing nodes flagged with their error code.
- `formatMinimalErrors(source, result)` — the **minimal erroring
  subexpressions**: nodes that failed while all their children succeeded,
  reported with source span, original text, and reason. Ancestors of a failure
  are marked `PROPAGATED` and excluded, so `(v + m) * a / d` pinpoints `v + m`.

## Layout

- `src/units.ts` — exponent-vector algebra (`mulUnits`, `divUnits`, `powUnit`, …)
- `src/ast.ts`, `src/parser.ts` — AST with source spans, recursive-descent parser
- `src/checker.ts` — unit inference, derivation tree, minimal-error extraction
- `src/cli.ts` — demo runner
- `test/checker.test.ts` — algebra, parentheses, negative powers,
  dimensionless results, zero-literal rule, minimal-error tests
