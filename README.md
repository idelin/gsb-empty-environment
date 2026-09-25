# tri-policy

A three-valued policy language in TypeScript. Missing attributes evaluate to
`unknown` (Kleene logic) and are **never** simplified to `false`. Policies
are compiled into a shared decision DAG with short-circuit evaluation,
read-dependency tracking, partial evaluation, and dependency-scoped cache
invalidation. Policies may only read explicitly named attributes and combine
them via finite-set membership, integer comparisons/ranges, and boolean
operators — no external identity services.

## Layout

- `src/tri.ts` — three-valued (`true` / `false` / `unknown`) Kleene logic.
- `src/expr.ts` — policy AST, builders, naive reference evaluator, printer.
- `src/dag.ts` — hash-consing compiler: identical subtrees become shared DAG
  nodes; child order is preserved so short-circuit read behavior is unchanged.
- `src/partial.ts` — partial evaluation: folds known attributes, emits the
  residual condition over the remaining ones (`unknown` stays `unknown`).
- `src/store.ts` — async, versioned attribute store with read hooks,
  one-shot fault injection, and abort support.
- `src/engine.ts` — `PolicyEngine`: short-circuit DAG evaluation with
  per-request read memoization, version-consistency checks (retry, then
  `conflict` — never a mixed-version allow), a decision cache invalidated by
  attribute/rule dependencies, and minimal explanation slices.

## Semantics highlights

- `x OR NOT x` is `unknown` when `x` is missing — the excluded middle does
  not hold, and `unknown` is never collapsed to `false`.
- `false AND <expensive>` short-circuits: the expensive attribute is never
  read. Reads are memoized per request, so each attribute is fetched at most
  once per evaluation and the whole request sees one version snapshot.
- If an attribute version changes mid-request, the evaluation is retried;
  if no consistent snapshot emerges, the result is `conflict` — never an
  allow computed from mixed versions.
- Cache entries are keyed by `(rule, subject)` and carry their read
  dependencies plus observed versions. An attribute hot-update evicts only
  entries that actually read it; a rule hot-update evicts only that rule.
  Failures and cancellations cache nothing.
- Every decision returns a minimal explanation slice: for `AND = false` only
  the falsifying branch, for `OR = true` only the succeeding branch, for
  `unknown` only the unknown branches.

## Running the tests

Requires Node >= 22.6 (uses built-in type stripping and `node:test`;
developed against Node v23.9.0). No npm dependencies.

```sh
node --test test/*.test.ts
# or
npm test
```

See `RESULTS.md` for the recorded output of an actual run.
