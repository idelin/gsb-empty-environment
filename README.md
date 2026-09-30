# tier-select-bnb

Exact-arithmetic selection of component precision tiers minimizing total cost
subject to a worst-case total error budget, with correlation-aware error
bounds, branch and bound, and independently verifiable solution certificates.

Requires Node.js >= 23 (runs TypeScript directly via type stripping; no
build step, no dependencies).

## Model

- **Integer tiers** — each component has an ordered list of tiers
  (`cost`, `error`). The tier *index* is a purely combinatorial choice.
- **Interval error direction** — a tier's `error` is an interval *radius*
  `e >= 0`; the actual error is `delta * e` with adversarial
  `delta in [-1, +1]`. Tier indices and interval directions are separate
  concepts and never mixed.
- **Sensitivities** — rational, may be negative. For independent error
  variables the worst case uses `|a| * e` (negative sensitivities count by
  absolute contribution).
- **Error sources** —
  - `shared` (e.g. calibration): one coherent variable drives all
    referencing components. Worst case `= |sum_i a_i * e_i|` — a signed sum,
    so correlated cancellation is accounted for. The naive
    `sum_i |a_i| * e_i` is printed for contrast only and is **never** used
    as (or claimed to be) the tight bound.
  - `independent`: each component's interval is independent, worst case
    `= sum_i |a_i| * e_i` (tight under independence).
  - Total worst-case error = sum of per-source worst cases.
- **Constraints** — mutually exclusive tier pairs (`exclusions`) and
  implication tiers (`dependencies`: selecting tier X forces tier Y).

## Exact arithmetic and rounding

- All values are exact rationals (`BigInt` numerator/denominator, always
  normalized). Decimal strings are parsed digit-by-digit into `BigInt`;
  **no `Number` intermediary** is ever used for values, so quantities
  beyond 2^53 or with many decimal places stay exact.
- Feasibility and bound decisions use exact comparison only.
- Rounding exists solely in display (`toDecimalString`): fixed decimal
  places, **round-half-away-from-zero**, stated explicitly here and in code.

## Solver

- **Branch and bound** (`src/solve.ts`): cost lower bound = committed cost
  + sum of cheapest remaining tiers; error pruning uses an optimistic
  achievable-error bound per source (for shared sources,
  `max(0, |C| - R)` with `R` the remaining shift range).
- **Exhaustive enumeration** (`src/exhaustive.ts`) cross-checks small
  instances independently of the search code.
- Outcomes: `optimal` (lower bound == achieved cost), `infeasible`
  (proven; lower bound +inf), `undecided` (node budget exhausted — a
  feasible upper bound and a proven lower bound are reported and optimality
  is **not** claimed).

## Certificates

`src/certificate.ts` issues a certificate with the instance hash (which
covers the objective, i.e. the error budget), the selection, exact cost,
the full per-source error breakdown, and the proven lower bound.
`verifyCertificate` re-checks everything from the instance alone:
hash (any objective change => **stale** => invalid, re-solve required),
constraints, exact cost/error recomputation, bound ordering, and — for
small instances — re-proves optimality by exhaustive enumeration.

## Usage

```sh
npm test                                  # node --test "test/*.test.ts"
node src/cli.ts samples/demo.json --exhaustive
node src/cli.ts samples/demo.json --budget=1     # undecided, bounds only
node src/cli.ts samples/tiny.json --json         # includes certificate JSON
```

Instance format: see `samples/demo.json`. All numbers are decimal strings.

## Sample run

`node src/cli.ts samples/demo.json --exhaustive`:

```
instance: sensor-chain-demo
error budget: 0.050000 (1/20)
status: optimal
nodes: explored=32 pruned(cost)=9 pruned(error)=9 leaves=3

selection (feasible upper bound):
  sensor = mid   cost 20.000000 (20)   tier error radius 0.008000 (1/125)
  amp = low   cost 5.000000 (5)   tier error radius 0.015000 (3/200)
  adc = low   cost 6.000000 (6)   tier error radius 0.012000 (3/250)
  ref = low   cost 4.000000 (4)   tier error radius 0.010000 (1/100)
total cost (upper bound): 35.000000 (35)

error breakdown by source (correlation-aware worst case):
  source cal [shared]
    sensor=mid: a=6/5 e=1/125 -> signed contribution 6/625
    amp=low: a=-4/5 e=3/200 -> signed contribution -3/250
    adc=low: a=1/2 e=3/250 -> signed contribution 3/500
    ref=low: a=1 e=1/100 -> signed contribution 1/100
    signed sum: 17/1250
    worst case for source: 17/1250
    (naive independent sum would be 47/1250 — reported for contrast, NOT used as the bound)
  source n_sensor [independent]   worst case for source: 3/625
  source n_amp [independent]      worst case for source: 3/200
  source n_adc [independent]      worst case for source: 27/2500
  source n_ref [independent]      worst case for source: 1/250
total worst-case error: 0.048200 (241/5000) <= budget: true

bounds: lower (proven) = 35.000000 (35) ; upper (feasible) = 35.000000 (35)

exhaustive cross-check: 81 combinations, 46 feasible; best cost 35.000000 (35)
cross-check: B&B cost 35 vs exhaustive 35 -> MATCH

certificate verification: valid=true stale=false optimality=exhaustive-confirmed
```

Note: the all-cheapest selection (cost 23) has worst-case error
349/5000 = 0.0698 > 0.05, so it is infeasible; the optimum is 35.

Budget exhaustion (`--budget=1`):

```
status: undecided (node budget exhausted — optimality NOT claimed)
bounds: lower (proven) = 23.000000 (23) ; upper (feasible) = none
```

## Test results

`npm test` (node:test, 26 tests, all passing):

```
✔ parse is exact: 0.1 + 0.2 === 0.3 (unlike IEEE-754)
✔ no Number intermediary: integers beyond 2^53 stay exact
✔ normalization: sign to numerator, gcd reduced
✔ arithmetic and comparison
✔ scientific notation parses exactly
✔ display rounding: half away from zero, display only
✔ shared source: correlated signed sum, NOT independent sum of absolutes
✔ independent source: negative sensitivity counts by absolute value
✔ total is sum of per-source worst cases; feasibility is exact
✔ cheapest tier everywhere can exceed the budget
✔ exclusions and dependencies are detected
✔ selection cost is exact
✔ B&B optimum matches exhaustive enumeration (demo)
✔ B&B optimum matches exhaustive enumeration (tiny)
✔ node budget exhaustion: undecided, bounds reported, optimality not claimed
✔ partial budget: incumbent may exist, still undecided, LB <= UB
✔ infeasible instance: proven, lower bound is +infinity (null)
✔ dependency forces expensive tier when antecedent selected
✔ exclusion forbids the otherwise-cheapest pair
✔ randomized small instances: B&B always matches exhaustive
✔ certificate for optimal solution verifies, optimality independently confirmed
✔ objective change makes the old certificate stale (re-verification fails)
✔ tampered selection is rejected
✔ tampered error report is rejected
✔ budget-exhausted certificate: verifies as undecided, claims no optimality
✔ infeasible certificate verifies
ℹ tests 26  ℹ pass 26  ℹ fail 0
```
