# cartesian-expander

Streaming expansion of a reference template over Cartesian parameter axes,
with finite-domain constraints, declared-identity dedup, budgets and
resumable cursors. Pure TypeScript on Node.js (>= 22.6, type stripping),
no dependencies; tested with the built-in `node:test` runner.

## Model

- Input is a JSON-compatible tree. Object keys are unordered (canonicalized
  with sorted keys), arrays are ordered.
- `template`: any JSON tree; `{ "$param": "axisName" }` nodes are substituted
  with the current axis value.
- `axes`: an **array** (declaration order matters) of
  `{ name, domain, version? }`. The last axis varies fastest, so a 2x3 input
  yields exactly 6 records in a deterministic order. An empty domain yields
  0 records.
- `constraints`: `implies` (finite-domain dependency) and `mutex` (mutual
  exclusion) over tri-state conditions (`equals` / `in` / `not` / `all` /
  `any`). One-step forward checking prunes remaining domains as soon as a
  partial assignment is fixed, so infeasible subtrees are skipped without
  enumerating them.
- `identity`: record paths (e.g. `$.user.id`) declaring the dedup identity.
  Duplicates collapse to their first occurrence, streaming, via a `Set` —
  the full product is never materialized.

## API

| Function | Purpose |
| --- | --- |
| `estimate(input)` | Cheap upper bound: product of domain sizes, O(#axes). |
| `count(input)` | Exact emitted-record count (constraints + dedup applied). |
| `expand(input, {budget, cursor})` | Collect up to `budget` records. |
| `stream(input, {budget, cursor})` | Lazy generator of `{record, rank}`. |
| `at(input, rank)` | Record at 0-based rank; negative ranks count from the end. |
| `atReverse(input, k)` | Record at reverse rank (`k=0` is last). |
| `verifyCompleteness(input, max?)` | Cross-check vs naive full-product expansion (small domains). |

## Budgets and cursors

`expand` with a `budget` stops after that many records and returns
`{ complete: false, total: null, cursor }`. `total` is only reported when the
enumeration actually finished — a truncated run never fabricates a total.
The cursor encodes the odometer position of the first un-emitted feasible
record plus the dedup state, so resuming preserves unexplored branches
exactly. The cursor is bound to a SHA-256 digest of the canonicalized
template, template/parameter versions, axes, constraints and identity:
changing any parameter (or bumping a `version`) makes the old cursor
unresumable (`ExpandError` with `source: "cursor"`).

## Errors

Every failure is an `ExpandError` carrying `source`
(`template` / `params` / `constraints` / `identity` / `cursor`) and a
concrete JSON `path` such as `$.template.items[1].bad.$param`.

## Run

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## Layout

- `src/json.ts` — canonicalization, digest, JSON paths
- `src/spec.ts` — input validation + binding digest
- `src/constraints.ts` — tri-state conditions, violation, forward checking
- `src/template.ts` — template rendering, identity extraction
- `src/engine.ts` — streaming odometer, budget/cursor, dedup
- `src/index.ts` — public API
- `src/verify.ts` — naive full-product reference + completeness check
- `test/` — node:test suites, incl. exhaustive small-domain verification
- `examples/demo.ts` — runnable feature tour
