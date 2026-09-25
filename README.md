# graded-policy-engine

Rule-based grading over partially-known, finite-domain attributes.

- **Rules** carry exact rational weights (BigInt-backed) and/or `hardReject`.
- **Bands** are explicit ascending score thresholds mapping a score to a grade.
- **Missing attributes** range over their declared finite domains. The joint
  assignment space is enumerated, so unknowns shared by several rules stay
  correlated — mutually exclusive conditions can never fire together and the
  reported score bounds are exact, never per-term interval extrema.
- **Results** are `definite` (one reachable outcome) or `pending` (reachable
  grade set and/or possible REJECT, exact score bounds, and one witness
  assignment per reachable outcome). An interval straddling a threshold is
  reported as pending — never decided by midpoint.
- **Hard rejects dominate**: no score, however high, offsets a fired
  `hardReject` rule.
- **Duplicate rule ids**: identical content is counted once; conflicting
  content is rejected with `PolicyConflictError`.
- **Policies are closed worlds**: conditions may only read declared
  attributes, using integer comparisons, finite-set membership and boolean
  combinators. No external identity/enrichment calls.
- **Incremental recompute**: `GradingSession` caches static rule results per
  dependency and reuses the joint enumeration unless the unknown set or a
  feeding known value changed, so attribute corrections are cheap.

## Layout

- `src/rational.ts` — exact rational arithmetic
- `src/condition.ts` — condition AST, validation, canonical form
- `src/policy.ts` — policy normalization, duplicate-id handling, bands
- `src/engine.ts` — joint enumeration, reachable outcomes, witnesses, bounds
- `src/session.ts` — incremental recompute session
- `test/reference.ts` — independent brute-force reference used by property tests
- `samples/demo.ts` — runnable end-to-end sample

## Requirements & usage

Node.js >= 22.6 (uses built-in type stripping; no build step, no dependencies).

```sh
npm test        # node --test test/*.test.ts
npm run sample  # node samples/demo.ts
```

## Minimal example

```ts
import { evaluate, normalizePolicy } from './src/index.ts';

const policy = normalizePolicy({
  attributes: { x: [0, 1] },
  rules: [
    { id: 'swing', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: '2/3' },
    { id: 'deny', when: { kind: 'cmp', attr: 'x', op: 'lt', value: 0 }, hardReject: true },
  ],
  bands: [
    { grade: 'declined', gte: null },
    { grade: 'standard', gte: '1/2' },
  ],
});

evaluate(policy, {});       // x missing -> pending: declined | standard, bounds [0, 2/3]
evaluate(policy, { x: 1 }); // definite: standard, score 2/3
```
