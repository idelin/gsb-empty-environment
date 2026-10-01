# rational-approx

Best rational approximation `p/q` with `q <= maxDen`, computed with continued
fractions on **exact BigInt rationals** — no value ever passes through
`Number`, so 40-digit decimals and `maxDen = 10^30` stay exact.

Zero dependencies. TypeScript executed directly by Node's type stripping.

## Requirements

- Node.js **>= 23.6**: run everything as-is.
- Node.js 22.6–23.4: add `--experimental-strip-types` to the commands below.

```sh
node examples/demo.ts   # run the samples
node --test             # run the test suite (node:test)
```

## Features

- **Continued fractions + semiconvergent jump**: convergents are walked until
  the next denominator would exceed `maxDen`, then the best semiconvergent is
  reached in one jump, `t = floor((maxDen - q_{k-1}) / q_k)`. Cost is
  O(log maxDen) Euclidean steps even for `maxDen = 10^30`.
- **Exact rational targets**: decimal strings (`"3.14"`, `"1e-3"`) and
  fractions (`"22/7"`) are parsed exactly — parsing never rounds.
- **Interval targets with refinable precision**: the target may be an exact
  interval `[lo, hi]` (e.g. a rounded measurement). A definite answer is
  returned only when *every* target in the interval shares the same unique
  optimum; otherwise the competing candidates and the undecided evidence
  (errors at both endpoints, equidistant decision boundaries) are returned.
- **Budget interruption**: every algorithm accepts a `Budget`; when it runs
  out, a structured `budget-exhausted` result with partial progress is
  returned instead of throwing.
- **Exhaustive reference search** (`O(maxDen)`) used by the tests to
  cross-validate optimality and tie-breaking on small bounds.

## Explicit rules

**Tie-breaking (决胜)** among equidistant candidates, in order:

1. smaller `|x - p/q|` wins;
2. tie → smaller denominator wins;
3. tie → smaller `|numerator|` wins (closer to zero);
4. tie → larger numerator wins (total-order backstop, unreachable in practice).

**Negatives**: solved on `|x|` and mirrored, so `best(-x) = -best(x)` exactly.

**Exact targets**: if `x = p/q` with `q <= maxDen`, the result is `x` itself
with error `0`. `maxDen = 1` returns the best integer per the rules above.

**Rounding**: approximation itself never rounds. Only *display* rounds, via
`formatDecimal(value, places, mode)` with explicit modes `"half-up"`
(half away from zero), `"half-even"` (banker's), `"truncate"` (toward zero).

**Interval decision criterion** (sound and complete): the optimum is constant
over `[lo, hi]` iff the best approximation is the *same unique* candidate at
both endpoints — for any rival `s > r`, `|x-r| - |x-s|` is non-decreasing in
`x`, so `r` beating all rivals at both endpoints settles the whole interval.

## API sketch

```ts
import {
  bestApprox, bestApproxExhaustive, approximateInterval,
  intervalFromRoundedDecimal, makeInterval, parseDecimal, makeRational,
  Budget, formatDecimal,
} from "./src/index.ts";

bestApprox(parseDecimal("3.14159265358979"), 100n);
// { status: "ok", value: 311/99, error: ..., exact: false, tied: [311/99] }

approximateInterval(intervalFromRoundedDecimal("3.1"), 7n);
// { status: "undecided", candidates: [3/1, 22/7], evidence: [...], boundaries: [43/14] }

bestApprox(pi, 10n ** 30n, new Budget(3));
// { status: "budget-exhausted", partial: { lastConvergent: 333/106 } }
```

## Sample run (`node examples/demo.ts`)

```
== 1. pi under growing denominator bounds (continued fractions) ==
  maxDen=1: 3/1  error ≈ 0.141592653590
  maxDen=7: 22/7  error ≈ 0.001264489267
  maxDen=100: 311/99  error ≈ 0.000178512176
  maxDen=113: 355/113  error ≈ 0.000000266764
  maxDen=1000000: 3126535/995207  error ≈ 0.000000000001

== 2. huge bound 10^30 (semiconvergent jump, BigInt only) ==
  maxDen=10^30: 1283867503676418762602531456701/408667718970308307878479074670
                error=31/255417324356442692424049421668750000000000000000000000000000000

== 3. negatives, integers, exact rationals ==
  -pi, maxDen=100: -311/99  (same error as +pi)
  355/113, maxDen=113: 355/113  error=0/1  (exact)
  42, maxDen=1: 42/1  error=0/1  (exact)

== 4. equidistant tie-breaking ==
  1/2, maxDen=1: 0/1   tied=[0/1, 1/1]
  7/2, maxDen=1: 3/1   tied=[3/1, 4/1]
  -7/2, maxDen=1: -3/1 tied=[-3/1, -4/1]

== 5. interval target, refined by more digits (maxDen=7) ==
  [3.1, 3.2]: UNDECIDED — candidates 22/7 (wins at lo) vs 16/5 (wins at hi),
              decision boundary 111/35 inside the interval
  "3.1"  rounded [3.05, 3.15]: UNDECIDED — 3/1 vs 22/7, boundary 43/14
  "3.14" rounded: DECIDED 22/7  worstError=11/1400
  "3.1416" rounded: DECIDED 22/7  worstError=183/140000

== 6. budget interruption ==
  pi, maxDen=10^30, budget=3: BUDGET EXHAUSTED (last convergent: 333/106)
  exhaustive pi, maxDen=10^6, budget=50000: BUDGET EXHAUSTED after 50000
      denominators, best so far 104348/33215

== 7. explicit display rounding of 1/8 at 2 places ==
  half-up: 0.13   half-even: 0.12   truncate: 0.12
```

## Test results (`node --test`, Node v23.9.0)

31 tests, all passing (~17 s), including:

- dense cross-validation of the continued-fraction solver against the
  exhaustive reference: all `k/m` with `m <= 40`, `-80 <= k <= 80`
  (negatives and integers included) at `maxDen ∈ {1, 2, 7, 30}`, plus 200
  deterministic pseudo-random cases with `maxDen` up to 2000, plus the
  36-digit pi decimal at `maxDen = 100000` — winner, error and full tie-set
  must all match;
- equidistant tie-breaking (`1/2`, `7/2`, `-3/2` at `maxDen = 1`);
- semiconvergent-jump region of `1000/3001 = [0; 3, 1000]` cross-checked
  denominator by denominator;
- interval decided / undecided / endpoint-tie / refinement / negative /
  degenerate cases;
- budget interruption for all three solvers;
- exact decimal parsing (40 digits, no `Number`) and the three explicit
  display-rounding modes.

```
ℹ tests 31
ℹ pass 31
ℹ fail 0
```

## Layout

- `src/rational.ts` — BigInt rational arithmetic, exact parsing, display rounding
- `src/ordering.ts` — the explicit tie-breaking comparator
- `src/continuedFraction.ts` — CF solver with semiconvergent jump
- `src/exhaustive.ts` — O(maxDen) reference search
- `src/interval.ts` — interval targets, decided/undecided + evidence
- `src/budget.ts` — operation budget
- `examples/demo.ts` — the sample run above
- `tests/` — `node:test` suite
