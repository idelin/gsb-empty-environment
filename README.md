# policy-conflict-analyzer

Detects inputs where **allow** and **deny** rules apply simultaneously, over
finite enum domains and bounded integer domains. Pure TypeScript, zero
dependencies, verified with the Node.js built-in test runner
(`node --test`, Node ≥ 22.6 runs `.ts` directly via type stripping).

## Model

- A **schema** explicitly declares every attribute a policy may read —
  either a finite set (`enum`) or a bounded integer (`int`). Conditions
  referencing undeclared attributes are rejected; no external identity
  service is consulted.
- A **condition** is built from `in` (finite-set membership), `cmp`
  (integer `< <= == >= > !=`) and boolean `and` / `or` / `not`.
- A **rule** is `{ id, effect: 'allow' | 'deny', when: Cond }`.

## How it works

1. **DNF normalization with shared-subexpression reuse.** Conditions are
   lowered to DNF over *cubes* (per-attribute: one closed integer interval
   or one value subset). Results are memoized by canonical structural key,
   so identical subexpressions across rules are solved only once
   (`stats.dnfCacheHits`).
2. **Exact boundary semantics.** Comparisons are normalized to closed
   integer intervals (`x < 5` → `[min, 4]`), so mutually exclusive ranges
   (`x < 5` vs `x >= 5`) never produce false conflicts, and boundary
   equality overlaps only when both sides are closed (`x <= 5` vs `x >= 5`
   conflicts exactly at `5`).
3. **Conflict pairs + incremental recompute.** Each allow×deny pair is
   intersected once and cached. `removeRule(id)` retracts a rule and drops
   only the cache entries involving it; the next `analyze()` reuses
   everything else (`stats.pairComputations` does not grow).
4. **Normalized disjoint regions.** Conflict cubes are merged into
   pairwise-disjoint regions via cube subtraction (interval splitting for
   integers, set difference for enums). Each region gets its **own**
   minimal counterexample (lexicographically smallest point), so distinct
   regions are never masked by a single representative point.
5. **Report.** Per region: constraint description, covering allow/deny
   rule ids, and a witness instance. Globally: minimal conflict rule sets
   (overlapping allow/deny pairs) and the involved rule ids.
6. **Independent enumeration cross-check.** For finite small domains
   (`domain size ≤ maxEnum`) every input is enumerated and evaluated with
   a separate plain evaluator; any disagreement with the region analysis
   is reported as a mismatch.
7. **Budget honesty.** `maxRegions` caps the region normalization,
   `maxEnum` caps the enumeration. When a budget cuts the search short,
   the report is `status: 'partial'` with a `pendingReason` (and
   `verification.pending: true` for enumeration): unvisited areas are
   **undecided**, never silently claimed conflict-free.

## Usage

```ts
import { ConflictAnalyzer } from './src/index.ts';

const analyzer = new ConflictAnalyzer({
  role: { kind: 'enum', values: ['admin', 'user', 'guest'] },
  age:  { kind: 'int', min: 0, max: 120 },
});
analyzer.addRule({ id: 'A1', effect: 'allow',
  when: { op: 'in', attr: 'role', values: ['admin', 'user'] } });
analyzer.addRule({ id: 'D1', effect: 'deny',
  when: { op: 'and', args: [
    { op: 'in', attr: 'role', values: ['user'] },
    { op: 'cmp', attr: 'age', cmp: '<', value: 21 },
  ] } });

const report = analyzer.analyze();
// report.regions[0].description -> { role: 'user', age: '0..20' }
// report.regions[0].witness     -> { role: 'user', age: 0 }
// report.minimalConflictSets    -> [{ allow: 'A1', deny: 'D1' }]
// report.verification           -> { mode: 'exhaustive', mismatches: [], ... }

analyzer.removeRule('D1');        // retract by id
analyzer.analyze();               // incremental: cached pairs are reused
```

## Run

```sh
npm test     # node --test (15 tests)
npm run demo # examples/demo.ts
```

## Demo output (excerpt)

```
=== initial analysis ===
status: complete
conflict regions (normalized, pairwise disjoint):
  #0 { role=editor, tier=*, age=0..20 }
      allow: [allow-staff] deny: [deny-minor-editor]
      minimal counterexample: {"role":"editor","tier":"free","age":0}
  #1 { role=admin, tier=free, age=* }
      allow: [allow-staff] deny: [deny-free-tier]
      minimal counterexample: {"role":"admin","tier":"free","age":0}
  #2 { role=editor, tier=free, age=21..120 }
      allow: [allow-staff] deny: [deny-free-tier]
      minimal counterexample: {"role":"editor","tier":"free","age":21}
verification: exhaustive, checked 1089/1089 points, mismatches: 0

=== after retracting deny-free-tier (incremental) ===
status: complete
  #0 { role=editor, tier=*, age=0..20 }
stats: ... pair computations=4        <- unchanged: pairs were reused

=== budget-limited (maxRegions=2, maxEnum=500) ===
status: partial (region budget 2 exhausted; remaining conflict candidates are undecided)
verification: partial, checked 500/1001 points (PENDING: budget too small, ...)
```

## Test results

```
✔ detects overlap of allow and deny with interval and enum constraints
✔ mutually exclusive intervals are not reported as conflicts
✔ boundary equality overlaps only when both sides are closed
✔ boolean operators (and/or/not) and != are handled
✔ regions are pairwise disjoint and each gets its own witness instance
✔ finite small domains are independently enumerated and all conflicts verified
✔ rule retraction by id with incremental recompute (pair cache reuse)
✔ shared condition subexpressions reuse the DNF cache
✔ insufficient region budget marks the search partial, never silently conflict-free
✔ insufficient enumeration budget marks verification pending
✔ policies may only read explicitly declared attributes
✔ minimal conflict rule sets only include genuinely overlapping pairs
✔ no rules or one-sided rules yield a complete empty report
✔ witness instances satisfy both allow and deny conditions
✔ partial search: uncovered conflict points are undecided, not mismatches
ℹ tests 15  ℹ pass 15  ℹ fail 0
```

## Layout

- `src/types.ts` — schema, condition, rule, report types
- `src/interval.ts` — closed integer interval algebra
- `src/cube.ts` — cube intersection / subtraction / witnesses
- `src/dnf.ts` — validation + DNF normalization with shared cache
- `src/evaluate.ts` — independent point evaluator (cross-check)
- `src/analyzer.ts` — `ConflictAnalyzer` (pairs, regions, budget, verification)
- `test/analyzer.test.ts` — `node:test` suite
- `examples/demo.ts` — runnable sample
