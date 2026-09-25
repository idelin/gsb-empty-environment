# policy-scope-containment

Decide whether one policy's applicability scope **contains** another's over an
**explicit finite domain**, with a **minimal counterexample** when it does not.

Policies are pure read-only conditions over explicit attributes — finite-set
predicates, integer conditions (comparisons and multi-variable difference
constraints) and boolean combinations. No external identity services.

## Condition language

| Constructor | Meaning |
| --- | --- |
| `cmp(x, op, n)` / `ge, gt, le, lt, eq, ne` | integer comparison `x ⋈ n` |
| `diff(x, op, y, c)` | difference constraint `x − y ⋈ c` |
| `inSet(x, {v…})` | finite-set membership |
| `and / or / not` | boolean combinations (Kleene three-valued) |
| `intersect / union / difference` | set-algebra sugar over scopes |

Domains declare every attribute finitely: integer intervals
(`{kind:'int', lo, hi}`) or explicit value sets (`{kind:'enum', values}`),
optionally `optional: true` to allow the attribute to be **unknown (⊥)**.

## Semantics

- **Three-valued**: predicates over unknown attributes evaluate to `U`
  (Kleene strong logic). `contains(A, B)` holds iff no domain point makes `B`
  definitely true while `A` is not definitely true (`F` or `U`).
- **Unknown domain**: a condition referencing an attribute with no declared
  domain yields `undecided` (`unknown-domain`) — containment cannot be proven.
- **Budget**: evaluation is step-budgeted; exhaustion yields `undecided`
  (`budget`) rather than a wrong answer.
- **Minimal counterexample**: lexicographically smallest violating point
  (domain declaration order, `⊥` smallest).

## Engine

- Conditions are **hash-consed** into a shared DAG; abstract evaluations are
  memoized per (node, cell).
- The domain is refined as a product of **interval / value-set cells**:
  leaf predicates are decided exactly on whole boxes, clean cells are pruned,
  and only undecided cells are split (at predicate boundary constants, else
  bisection) — no per-value enumeration in the decision path.
- `src/verify.ts` is an **independent brute-force checker** that exhaustively
  enumerates small domains to validate containment verdicts, counterexample
  validity and minimality, and the intersection / union / difference algebra.

## Usage

```ts
import { contains, ge, type Domain } from './src/index.ts';

const domain: Domain = { x: { kind: 'int', lo: 0, hi: 10 } };
contains(ge('x', 3), ge('x', 2), domain);
// { status: 'not-contained', counterexample: { x: 2 } }
```

## Run

```sh
npm test    # node --test (Node >= 23, native TypeScript type stripping)
npm run demo
```
