# best-rational-approx

Best rational approximation `p/q` to a target with `q <= maxDen`, computed
with **exact BigInt arithmetic only** — no value ever passes through
`Number`, so 60-digit targets and `maxDen = 1e40` are handled exactly.
Written in TypeScript, executed directly by Node.js (>= 22.18 / 23.6) type
stripping; zero dependencies, tested with the built-in `node:test` runner.

## Targets

A target is either

- a **point**: an exact rational, parsed from `"3.14159..."`, `"-0.5"`,
  `"22/7"` (arbitrary precision, digits go straight into BigInt), or
- an **interval** `[lower, upper]`: the true value is only known to lie
  inside (e.g. a measurement whose precision can be refined later).

## Rounding / tie-breaking rules (explicit, total order)

Between two candidates for the same target `x`:

1. smaller error `|x - p/q|` wins;
2. equal error → **smaller denominator** wins;
3. still equal → **smaller numerator** wins (toward `-∞`).

Consequences: exact halves round down — `0.5 → 0`, `2.5 → 2`, `-0.5 → -1`
at `maxDen = 1`. An exactly representable target returns **error 0**
(`bestApprox(parseRational("22/7"), 7n)` → `22/7, err 0`). `maxDen = 1`
returns the best integer under the same rules.

## Point targets: continued fractions + semiconvergent jump

`bestApprox(x, maxDen)` walks the convergents of `x` and performs a single
semiconvergent jump `k = (maxDen - q_{n-1}) / q_n` — cost is
`O(len(CF(x)))`, never `O(maxDen)`, so `maxDen = 1e30` is instant.
`bestApproxExhaustive` is the O(maxDen) reference implementation; the test
suite cross-checks them on dense sweeps (including negatives, integers,
exact halves) plus 300 randomized cases.

## Interval targets: decided / undecided / exhausted

`bestApproxInterval({lower, upper}, maxDen, {budget?})` returns:

- **`decided`** — one candidate is provably optimal for *every* target in
  the interval, with `bestError`/`worstError` over the interval.
  Theorem used: for fixed candidates `r, c` the set of `x` where `r` beats
  `c` is a half-line from their midpoint toward `r`; a half-line meeting
  the interval contains the nearer endpoint, so
  `bestApprox(lower) == bestApprox(upper)` already implies one shared
  optimum — no scan of the denominator space is needed.
- **`undecided`** — the live candidate set plus evidence. Every rational
  `p/q` (`q <= maxDen`) inside the interval is optimal at `x = p/q` itself,
  so the live set is exactly that Farey window (enumerated in
  `O(#candidates)` via a Farey walk, never `O(maxDen)`), plus the endpoint
  optima. Refine the interval and call again to converge to `decided`.
- **`exhausted`** — the optional step `budget` ran out during enumeration
  (e.g. a huge live set); returns the partial candidates found so far
  instead of a wrong answer.

## Usage

```bash
node examples/demo.ts   # runnable samples
node --test             # full test suite
```

```ts
import { bestApprox, bestApproxInterval, parseRational } from "./src/index.ts";

bestApprox(parseRational("3.14159265358979"), 100n);
// { best: 311/99, error: 3521/19850000 }  (exact rational error)

bestApproxInterval(
  { lower: parseRational("3.14158"), upper: parseRational("3.1416") },
  113n,
);
// { status: "decided", best: 355/113, bestError: 0, worstError: 807/56500000 }
```

## Sample run (`node examples/demo.ts`)

```
=== 1. Point targets: pi at escalating denominator bounds ===
  Q=                              1  3                  err=0.141592653589793238462643383279
  Q=                              7  22/7               err=0.001264489267349618680213759577
  Q=                            100  311/99             err=0.000178512175651824321229241865
  Q=                            113  355/113            err=0.000000266764189062422312368932
  Q=                          33102  103993/33102       err=0.000000000577890634390381888505
  Q=1000000000000000000000000000000  911500632578468251366565689480/290139662612505428515807449709 ...

=== 2. Negatives, integers, exact rationals, ties ===
  x=-3.14159265358979    Q=100  ->  -311/99  (err 0.000178512175)
  x=5                    Q=1    ->  5        (err 0)          x=0.5  Q=1 -> 0
  x=-22/7                Q=7    ->  -22/7    (err 0)          x=-0.5 Q=1 -> -1   x=2.5 Q=1 -> 2

=== 4. Interval targets with refinable precision (Q = 113) ===
  [3.1, 3.2]        UNDECIDED  candidates: 31/10, 338/109, ... (397 total)
  [3.14, 3.15]      UNDECIDED  candidates: 157/50, 336/107, ... (39 total)
  [3.1415, 3.1416]  UNDECIDED  candidates: 333/106, 355/113
  [3.14158, 3.1416] DECIDED    355/113  worstErr=0.000012920353

=== 5. Budget interruption on an astronomically large live set ===
  status=exhausted  (budget exhausted after 6 steps ...)
  22/7 +/- 1e-50, Q=1e40: status=decided  best=22/7
```

## Test results (`node --test`)

```
ℹ tests 28
ℹ pass 28
ℹ fail 0
```

Coverage highlights: exhaustive-reference cross-checks (dense sweep +
randomized), exact-rational error 0, `maxDen = 1` best integer, explicit
tie-breaks (equal error → smaller denominator → smaller numerator),
negatives and negation symmetry, huge `maxDen` semiconvergent jumps,
interval decided/undecided/exhausted, budget interruption, Farey
successor and one-sided ceiling against brute force, integer targets.
