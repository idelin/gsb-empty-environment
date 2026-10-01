# bigint-fixed-roots

Correctly rounded fixed-point **square roots** and **n-th roots** on `BigInt`,
in pure TypeScript, tested with the Node.js built-in test runner. No `Number`
conversion is ever applied to the values being rooted — all payload arithmetic
is exact `BigInt` arithmetic (`Number` is used only for loop caps and
bit-length bookkeeping).

Requires Node.js >= 23.6 (runs `.ts` directly via type stripping; no build
step, no dependencies).

## API

```ts
import { sqrtFixed, rootFixed, isqrtRem, irootRem, roundQuotient } from "./src/index.ts";
```

- `sqrtFixed(x, inScale, outScale, mode?, budget?)` — dedicated square-root
  **fast path** (shift-based integer Newton).
- `rootFixed(x, inScale, outScale, degree, mode?, budget?)` — general
  correctly rounded `degree`-th root (`degree >= 2`).
- `isqrtRem(x)` / `irootRem(x, k)` — exact integer roots with remainder.
- `roundQuotient(num, den, mode)` — exact rational rounding helper.

**Input/output precision are independent.** The input is the exact decimal
`x * 10^(-inScale)`; the result is the root rounded to `outScale` decimal
places, returned as an integer count of ulps (`1 ulp = 10^(-outScale)`).

Each fixed-point result carries:

| field | meaning |
| --- | --- |
| `rounded` | correctly rounded root, in ulps of `10^(-outScale)` |
| `floor` / `ceil` | exact integer bracket around the true scaled value |
| `exact` | true iff the scaled value is an integer |
| `remainder` | exact `A - floor^k * B` (0 iff exact) |
| `ulpErrorBound` | `"1/2"` for nearest modes, `"1"` for directed modes |
| `iterations` | Newton iterations used (termination audit) |

## Explicit rounding rules

`mode` is one of `"floor"`, `"ceil"`, `"halfUp"`, `"halfEven"`. There is no
implicit or hardware rounding anywhere. The midpoint decision

```
v >= floor(v) + 1/2   <=>   2^k * A  >=  (2*floor(v) + 1)^k * B
```

is an **exact integer power comparison**, where `v^k = A / B` with
`A = x * 10^max(k*outScale - inScale, 0)` and `B = 10^max(inScale - k*outScale, 0)`.
Exact ties are possible only when `B > 1` (e.g. `sqrt(0.25) = 0.5`); they are
resolved by the chosen rule (`halfEven` picks the even neighbour).

When `inScale > k*outScale` the input is **not** truncated first — the
denominator `B` is kept symbolically — so extremely small inputs such as
`sqrt(1e-100)` do not collapse to `0`.

## Termination proof (integer Newton)

For `isqrt` (the `iroot` case is identical with the weighted iterate
`y' = ((k-1)y + x/y^(k-1))/k`), with `s = floor(sqrt(x))` and
`y_{i+1} = floor((y_i + x/y_i)/2)`, `y_0 = 2^ceil(bitlen(x)/2)`:

1. **Lower-bound invariant.** AM-GM gives `(y + x/y)/2 >= sqrt(x)` for any
   `y > 0`, hence `y_{i+1} >= floor(sqrt(x)) = s`. Every iterate stays `>= s`.
2. **Strict decrease while `y_i > s`.** Then `y_i >= s+1`, so `y_i^2 > x`,
   so `x/y_i <= y_i - 1` (integer division), so
   `y_{i+1} <= floor((y_i + y_i - 1)/2) = y_i - 1 < y_i`.
3. **Termination.** A strictly decreasing integer sequence bounded below by
   `s` reaches the exit condition in finitely many steps. ∎

The loop **stops on non-decrease** (`y_{i+1} >= y_i`), never on equality of
consecutive iterates: for `x = k^2 - 1` (3, 8, 15, 24, ...) the orbit
alternates `s, s+1, s, s+1, ...` and an equality-based stop would never fire.
At exit, `y_{i+1} >= y_i` ⟺ `y_i^2 <= x` ⟺ `y_i <= s`, which with the
invariant gives `y_i = s` exactly. A defensive cap of `bitLength(x) + 8`
iterations (far above the quadratic-convergence requirement) turns any
violation of the proof into a loud error instead of a hang.

## Budget control

`RootBudget = { maxScale, maxBits }` (default `{ maxScale: 10_000, maxBits:
1_000_000 }`) rejects oversized decimal scales and oversized intermediate
operands with a `RangeError` before any big allocation happens.

## Recovery consistency

Computing at `outScale + G` guard digits and re-rounding the exact integer
quotient by `10^G` with the same rule reproduces the direct `outScale` result
exactly — verified by tests over all modes, including exact ties and perfect
powers.

## Verification highlights (node:test)

- exhaustive `isqrt` over `0..100000` vs `Math.sqrt` **and** the exact
  bracket `r^2 <= n < (r+1)^2`;
- square-boundary counterexamples `m^2 - 1, m^2, m^2 + 1` up to `2^1000`-scale;
- oscillation-prone inputs `k^2 - 1` (would hang an equality-based stop);
- exhaustive `iroot` for degrees 2..8 with the exact power bracket;
- perfect squares/powers exact (`remainder === 0`) at every output scale;
- exact decimal midpoints (`sqrt(0.25)`, `sqrt(6.25)`, `cbrt(0.125)`, ...)
  distinguishing `halfUp` from `halfEven`;
- tiny inputs (`sqrt(1e-100)`, `sqrt(0.5)`) never collapse to `0`;
- sqrt fast path agrees with the general n-th-root path on random inputs;
- guard-digit recovery reproduces the direct rounding result;
- property test: independent exact re-derivation of bracket + rounding
  decision on randomized inputs;
- budget limits enforced; negative inputs rejected (`RangeError`).

## Run

```console
$ npm test          # node --test 'test/*.test.ts'
$ npm run demo      # node examples/demo.ts
```

### Sample output (abridged)

```
== sqrt(2), 40 decimal places, round-half-even ==
  rounded   = 1.4142135623730950488016887242096980785697  (halfEven, ulp err <= 1/2)
  bracket   = [1.4142135623730950488016887242096980785696, 1.4142135623730950488016887242096980785697]  exact=false

== exact midpoint: sqrt(0.25) = 0.5 at 0 places ==
  halfEven -> 0   halfUp -> 1   bracket [0, 1]

== tiny input: sqrt(1e-100) at 50 places (no collapse to 0) ==
  rounded   = 0.00000000000000000000000000000000000000000000000001  exact=true

== integer square root with remainder ==
isqrt(10^30 + 12345) = 1000000000000000, remainder = 12345
```

### Test results

```
ℹ tests 26
ℹ pass 26
ℹ fail 0
```
