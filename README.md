# exact-apportion

Exact integer apportionment with rational weights, per-object and overlapping
group bounds, independent certificates, and minimal-move repair after weight
corrections. **BigInt / exact-decimal only — values never pass through
`Number`.** Requires Node.js >= 23.6 (runs TypeScript directly via type
stripping; no build step, no dependencies).

## Rules (all explicit)

**Ideal shares.** Weights are exact rationals (strings like `"0.3"` or
`"1/3"`, or `bigint`). They are scaled to a common denominator giving integer
numerators `N_i` with `S = sum(N_i)`. The ideal share of object `i` is the
exact rational `q_i = T * N_i / S`.

**Rounding (largest remainder baseline).** Each object gets `floor(q_i)`
(exact BigInt floor). The remaining `R = T - sum(floors)` units go one each to
the `R` largest remainders `r_i = T*N_i - floor(q_i)*S`. Ties on equal
remainders are broken by **stable ID** (ascending lexicographic id); input
order is irrelevant. So total 1 over three equal claims gives exactly one
object 1 unit: the one with the smallest id. The baseline is used only when
no extra constraints exist.

**Constrained solving.** Per-object lower/upper bounds and any number of
overlapping group min/max sums are **hard constraints enforced jointly**
(never group-by-group, so one group's allocation cannot break another's).
Among all feasible integer allocations the solver minimizes the exact total
deviation `sum_i |x_i*S - T*N_i|` from the ideal shares via branch-and-bound
(small-scale exact; a node limit guards the contract). Equal-cost ties prefer
the allocation that is lexicographically largest in ascending-id order, which
matches the baseline's stable-id tie rule.

**Infeasibility.** If bounds/groups cannot accommodate the total, the solver
returns `{ status: 'infeasible', deficit | surplus, details }` — it never
force-fits.

**Certificates.**
- *Conservation*: `verifyConservation` independently re-checks
  `sum(x) + pending === total`, every bound, and every group sum.
- *Optimality lower bound*: the relaxation that drops bounds and groups is
  solved in closed form by the largest-remainder method; its deviation is a
  certified lower bound on any feasible solution's deviation. Equality with
  the solver's objective proves optimality (`optimal: true`).
- *Enumeration proof*: for tiny instances an independent brute-force
  recursion (no shared search code) confirms the solver's cost tuple.

**Weight-correction repair.** `repairAllocation(problem, previous, { moveBudget })`
re-solves globally with lexicographic objective:
1. `pending` — unallocated units minimized first; if the move budget cannot
   even restore conservation, the shortfall stays **pending** instead of
   violating anything;
2. `deviation` — distance to the corrected ideal shares;
3. `moves` — `sum |x_i - previous_i|`, so equally optimal solutions use the
   fewest moves and there is no needless churn.

**Validation.** Negative weights and non-positive total weight are rejected;
duplicate ids, negative totals, and `lower > upper` are reported.

## Usage

```ts
import { solveApportion, repairAllocation } from './src/index.ts';

const r = solveApportion({
  total: 10n,
  items: [
    { id: 'alpha', weight: '0.5' },
    { id: 'beta',  weight: '0.3', upper: 2n },
    { id: 'gamma', weight: '0.2' },
  ],
  groups: [{ id: 'region', members: ['alpha', 'beta'], min: 6n }],
});
```

## Run

```sh
node --test test/*.test.ts     # test suite (node:test)
node examples/run-sample.ts    # runnable demo
```
