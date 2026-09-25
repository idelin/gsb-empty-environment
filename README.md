# Three-Valued Boolean + Interval Algebra Simplifier

A small, dependency-free TypeScript library (and CLI demo) that simplifies
policy expressions under **strong-Kleene three-valued semantics**, preserves
**provenance/source mapping**, and ships an **exhaustive equivalence tester**
that verifies every rewrite against the whole finite input domain.

## Requirements and how they are met

| Requirement | Where |
| --- | --- |
| Boolean + interval algebra simplification | `src/simplify.ts`, `src/ranges.ts` |
| Three-valued semantics (`TRUE` / `FALSE` / `MISSING`) | `src/trivalue.ts`, `src/eval.ts` |
| `x OR NOT x` must **not** become `true` on a nullable domain | no such rule exists; proven by `test/tester.test.ts` and demo section 1 |
| Constant `false` removes AND branches (source preserved) | `and-false-annihilator` rule |
| Before/after agree on **all** finite-domain inputs | `src/tester.ts` enumerates every assignment, including all "absent" ones |
| Read only explicitly declared attributes; finite sets, integer conditions, booleans | `src/schema.ts` (`assertDeclared`, `validateAssignment`) |
| No external identity service | nothing performs I/O; undeclared attributes throw `SchemaError` |
| Node.js built-in test runner | `node --test`, no third-party packages |

## Semantics

Attributes may be **absent** from an input. An absent leaf evaluates to
`MISSING`. Connectives follow strong Kleene logic:

- `FALSE AND x = FALSE`, `TRUE OR x = TRUE` (constants decide when they can);
- `TRUE AND MISSING = MISSING`, `FALSE OR MISSING = MISSING` (otherwise propagate);
- `NOT MISSING = MISSING`.

Therefore, for a missing boolean `x`, `x OR NOT x = MISSING`, **not** `TRUE` —
the law of excluded middle does not hold on a lossy/nullable domain. The same
applies to predicates: even a set that covers the whole declared universe
(e.g. `tier ∈ {free,pro,ent}`) stays a predicate, because an absent `tier`
makes it `MISSING` rather than `TRUE`.

## Implemented sound rules

Constant folding (`true AND x → x`, `false AND x → false`, `false OR x → x`,
`true OR x → true`, `NOT const`), double negation, De Morgan, associative
flattening, idempotence (`x AND x → x`), same-attribute interval
intersection/union (`age>=18 AND age<30 → age∈[18,29]`; `age<18 OR age>=65`),
finite-set intersection/union, and interval/set complement under `NOT`.
Every rule union-inherits the source ids of the nodes it consumed, and every
rule is independently exercised by the exhaustive tests.

Deliberately **not** implemented: absorption/distribution-style rewrites that
are not needed here, and any rewrite of `x OR NOT x` for nullable leaves.

## Layout

```
src/trivalue.ts   Kleene truth values and truth tables
src/ast.ts        typed expression AST + constructors + renderer
src/ranges.ts     discrete integer interval algebra (normalize/intersect/union/complement)
src/schema.ts     explicit finite/int/bool schema, finite-domain enumeration
src/eval.ts       three-valued evaluator
src/simplify.ts   rule-based simplifier + lineage entries
src/tester.ts     exhaustive equivalence + provenance tester
src/demo.ts       runnable examples
test/*.test.ts    node:test suites (incl. a 2000-case random fuzz)
```

## Run

Requires Node.js ≥ 22.6 (developed on Node 23) for native TypeScript type
stripping — no build step and no npm install are needed.

```sh
node --test                          # all suites
node --experimental-strip-types src/demo.ts   # worked examples
```

## Example

```
before: (age>=18 [a1] AND age<30 [a2])
after : age∈{18..29} [a1,a2]
EQUIVALENT — checked 4392/4392 finite-domain inputs
  lineage:
    - int-intersect: age: merged 2 interval(s) -> 1 box(es) <- {a1,a2}
```

Tempting unsound rewrite caught by the tester:

```
x OR NOT x  vs  true
NOT EQUIVALENT — checked 3672/3672 finite-domain inputs
  mismatch @ {…all attributes missing}: before=MISSING after=TRUE
```
