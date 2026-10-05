# tree-rewriter

A term tree rewriter in TypeScript with pattern variables, rule priorities,
leftmost-innermost evaluation, cycle detection, resumable budgets, and a
naive cross-check interpreter.

## Design

- **Terms** (`src/term.ts`) — immutable, hash-consed DAGs. Structurally
  equal subtrees are the same object; nodes are frozen and never mutated.
  Rewriting does path copying, so a shared subtree is only ever *logically*
  copied (O(1) reference reuse) and can never be polluted in place.
- **Matching** (`src/match.ts`) — patterns/templates with variables;
  repeated variables must bind structurally equal subterms.
- **Engine** (`src/engine.ts`) — rules are tried in priority order
  (ties keep declaration order); the redex is found leftmost-innermost
  (children left-to-right, then the node itself). Every step records the
  hit path, rule, variable bindings, redex, and resulting term. Repeated
  states (by canonical form) are reported as cycles with the step indices
  involved. `run(budget)` stops with status `"budget"` when exhausted and
  can be resumed by calling `run()` again, or serialized via
  `snapshot()` / `Engine.restore()`.
- **Naive interpreter** (`src/naive.ts`) — plain trees, no sharing, deep
  copies everywhere; same semantics, independent implementation.
- **Cross-check** (`src/verify.ts`) — `crossCheck()` runs both on the same
  input and asserts identical traces, statuses, cycle reports, and final
  terms.

## Usage

Requires Node >= 22.6 (runs TypeScript via type stripping).

```sh
node test/run.ts   # test suite
node src/demo.ts   # traced demo run
```

```ts
import { TermFactory, v, p, Engine } from "./src/index.ts";

const fac = new TermFactory();
const rules = [
  { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
];
const engine = new Engine(rules, fac.mk("add", [fac.mk("zero"), fac.mk("a")]), fac);
const res = engine.run(100); // budget-limited, resumable
console.log(res.status, engine.steps);
```
