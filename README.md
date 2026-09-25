# graded-policy-engine

Exact, dependency-free rule-engine in TypeScript: rational-weight scoring with
hard rejections, explicit segment thresholds, and exact analysis over missing
attributes on finite domains. Runs on Node.js ≥ 23.6 (native TypeScript type
stripping) with the built-in test runner — **no npm dependencies**.

## Semantics

- **Rational weights** — every weight/score is an exact rational (`bigint`
  numerator/denominator, e.g. `-25/2`); threshold comparisons never round.
- **Hard reject** — a fired hard-reject rule rejects the subject outright; no
  score, however high, offsets it.
- **Explicit thresholds** — grades are segments listed from highest to lowest
  with strictly decreasing inclusive lower bounds.
- **Missing attributes** — a missing attribute is an *unknown variable* over
  its declared finite domain (`int` range, `bool`, or `enum`). The evaluator
  enumerates the **joint** assignments of all unknowns, so conditions sharing
  the same variable stay correlated. Per-term independent extremes are
  unsound (see `naiveIndependentBounds`, included only to demonstrate the
  error) and midpoints are never used to decide a grade.
- **Definite vs undecided** — if every joint assignment yields the same
  outcome, the result is `definite`; otherwise `undecided` with the exact
  reachable outcome set, one witness assignment per outcome, exact score
  bounds and the exact reachable score set.
- **Duplicate rule ids** — identical content is deduplicated (counted once);
  conflicting content under the same id rejects the whole policy.
- **Policy surface** — conditions are limited to integer comparisons,
  membership in explicit finite sets, boolean tests and `and`/`or`/`not`.
  The engine reads only schema-declared attributes; there is no external
  identity service.
- **Incremental recompute** — `IncrementalEngine` keeps per-rule tri-state
  and the certain score; an attribute correction re-evaluates only rules
  that read the changed attributes, then re-enumerates only the remaining
  unknowns for the exact result.
- **Exhaustive reference** — `src/reference.ts` is an independent brute-force
  implementation; the test suite cross-checks the evaluator and the
  incremental engine against it on 200 randomized policies (negative weights,
  hard rejects, mutually exclusive conditions) plus witness validation.

## Layout

- `src/rational.ts` — exact rational arithmetic
- `src/schema.ts` — finite attribute domains and validation
- `src/condition.ts` — closed condition AST, tri-state eval, canonical form
- `src/policy.ts` — policy loading, thresholds, duplicate-id handling
- `src/evaluator.ts` — exact correlated evaluation over joint assignments
- `src/incremental.ts` — incremental engine for attribute corrections
- `src/reference.ts` — independent exhaustive reference for cross-checking
- `examples/sample.ts` — runnable end-to-end demo
- `test/` — `node:test` suites

## Run

```sh
node --version          # need >= 23.6
npm test                # node --test test/*.test.ts
npm run sample          # node examples/sample.ts
```

Sources use erasable-TypeScript syntax only, so Node runs them directly. If
you have network access, `npm i -D typescript && npx tsc --noEmit` adds a
static type-check pass (no `tsconfig` is required beyond defaults +
`--allowImportingTsExtensions`).
