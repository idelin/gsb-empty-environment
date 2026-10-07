# priority-rules-engine

A small TypeScript rule engine with a strict two-phase semantics:

1. **Snapshot judgement (phase 1)** — every rule's condition is evaluated
   against the *pristine input snapshot*. Assignments are collected but never
   written back, so **no rule can read another rule's assignment from the
   same round**.
2. **Per-field resolution (phase 2)** — for each assigned field, the highest
   `priority` wins. Ties at the top priority with *equal* values agree; ties
   with *different* values are reported as **conflicts** and the field keeps
   its snapshot value.

The engine returns a per-field **evidence tree** (which rules matched, which
candidate won / was shadowed / conflicted, and why), and ships a naive
reference model plus a checker that verifies **rule-order independence** by
comparing canonicalized results across permutations of the rule list.

## Usage

```ts
import { and, eq, gt, evaluate, renderEvidence, checkOrderIndependence, Rule } from './src';

const rules: Rule[] = [
  { id: 'vip-discount',     priority: 10, condition: eq('vip', true),      assigns: { discount: 0.2 } },
  { id: 'bulk-discount',    priority: 5,  condition: gt('items', 10),      assigns: { discount: 0.1 } },
  { id: 'holiday-discount', priority: 10, condition: eq('holiday', true),  assigns: { discount: 0.25 } },
  { id: 'express-shipping', priority: 3,  condition: and(eq('vip', true), gt('items', 5)),
    assigns: { shipping: 'express' } },
];

const snapshot = { vip: true, holiday: true, items: 12 };
const result = evaluate(rules, snapshot);

console.log(renderEvidence(result));
// discount: CONFLICT at priority 10
//   [conflict] holiday-discount priority=10 value=0.25
//   [conflict] vip-discount priority=10 value=0.2
//   [shadowed] bulk-discount priority=5 value=0.1
// shipping: "express"  <- express-shipping (priority 3)
//   [winner] express-shipping priority=3 value="express"

checkOrderIndependence(rules, snapshot);
// { independent: true, permutationsChecked: 24, ... }
```

## API

| Export | Purpose |
| --- | --- |
| `evaluate(rules, snapshot)` | Two-phase evaluation; returns `{ output, rules, fields, conflicts }`. |
| `renderEvidence(result)` | Renders the per-field evidence trees as text. |
| `evaluateNaive(rules, snapshot)` | Naive two-phase reference model (independent cross-check). |
| `canonicalize(result)` | Order-insensitive canonical form of a result. |
| `checkOrderIndependence(rules, snapshot, opts?)` | Runs the reference model over all permutations (≤8 rules) or a deterministic sample (larger sets) and reports any divergence. |
| `eq / ne / gt / ge / lt / le / isIn / exists / and / or / not / always / never / custom` | Condition combinators with structured `explain()` evidence. |

## Key types

- `Rule` — `{ id, priority, condition, assigns }`.
- `EvalResult` — `output` (snapshot + winners; conflicted fields keep their
  snapshot value), `rules` (phase-1 condition evidence per rule), `fields`
  (per-field evidence trees), `conflicts`.
- `FieldEvidence` — `status: 'resolved' | 'conflict' | 'untouched'`, plus
  `winner` / `conflict` details and the `candidates` list with per-candidate
  `outcome: 'winner' | 'agreed' | 'shadowed' | 'conflict'`.

## Canonicalization note

When several top-priority rules assign the *same* value, which one is
labelled `winner` vs `agreed` depends on rule order. `canonicalize`
collapses both to `topAgreed` and drops the winner's rule id (keeping the
order-independent value and priority), so order-independence checks compare
only semantically meaningful content.

## Development

No network access is required: the TypeScript compiler is vendored in
`node_modules/typescript` (with a thin `lib/tsc.js` driver around
`ts.executeCommandLine`).

```sh
npm test        # build + run tests
npm run build   # type-check and emit to dist/
node dist/examples/demo.js
```
