# Attribute Grammar Evaluator

A syntax-directed translation kernel in TypeScript: synthesized & inherited
attributes, dependency-driven topological evaluation, cycle detection, plus a
demo mini-language with scoped type inference.

**Requires Node.js >= 23.6** (runs TypeScript natively via type stripping; no
build step and zero dependencies).

## Layout

- `src/grammar.ts` — the kernel. Attributes are declared per AST node kind as
  `synthesized` or `inherited`; each attribute has one rule with a dependency
  list (`self` / `parent` / `child` / `childAt` / `direct` references). The
  evaluator expands every `(node, attribute)` pair into a dependency-graph
  vertex, topologically sorts it (Kahn), and evaluates rules in that order.
  Leftover vertices are reported as a `CycleError` naming the exact attribute
  instances and their source positions.
- `src/language.ts` — demo language semantics:
  - `env` (inherited): scope environment threaded top-down through the
    statement chain (`stmts[i].env = stmts[i-1].envAfter`).
  - `envAfter`, `type`, `value`, `output` (synthesized): computed bottom-up.
  - Type mismatches throw `LangError` carrying the precise AST node position.
- `src/parser.ts` — hand-written lexer + recursive-descent parser producing a
  positioned AST (`let` declarations, `print`, `if/else` blocks, integer and
  boolean expressions).
- `examples/sample.ts` — runnable demo (well-typed program + error reports).
- `test/` — Node.js built-in test runner (`node:test`).

## Run

```sh
node --test              # run the test suite
node examples/sample.ts  # run the sample programs
```

## Example

```
let x: int = 1 + 2;
let y = x * 10;        // type inferred as int
let ok: bool = y > 5;  // annotation checked against synthesized type
if ok { print y + x; }
```

Diagnostics point at the responsible node:

```
error: type mismatch: variable 'b' is declared as bool but initialized with int (at 3:1)
error: operator '+' expects int operands, got int and bool (at 3:12)
error: undefined variable 'y' (at 3:11)
```
