# Rational Bezier Extremum Locator

Exact extremum locator for 1D **rational Bezier curves** with weights, written in
TypeScript with **zero dependencies** (runs on Node.js >= 23 via native type
stripping). All arithmetic is exact rational arithmetic on `BigInt` — no
floating-point error anywhere in the pipeline.

## Math

For control values `y_0..y_n` and weights `w_0..w_n`:

```
y(t) = N(t) / D(t)
N(t) = sum_i w_i * y_i * B_i^n(t)
D(t) = sum_i w_i       * B_i^n(t)      (B_i^n = Bernstein basis)
```

Stationary points satisfy `y'(t) = 0`. Since `D(t) > 0` on `[0,1]` for positive
weights, this is equivalent to the **integer-coefficient polynomial equation**

```
P(t) = N'(t) D(t) - N(t) D'(t) = 0        (degree <= 2n - 2)
```

Root finding on `[0, 1]`:

1. **Rational roots** are found *exactly* via the rational root theorem and
   factored out (with multiplicities) by exact integer synthetic division.
2. **Irrational roots** are isolated into certified disjoint intervals with
   rational endpoints using **Sturm's theorem** (exact sign-variation counts)
   plus bisection, down to a configurable precision (default `2^-50`).
3. Sign changes of `P` across each root classify it as **max / min /
   stationary** (even-multiplicity roots do not change monotonicity).
4. Monotonicity segments and the **global max/min** (endpoints `t=0`, `t=1`
   always included as candidates) are reported. Values at rational roots are
   exact fractions; values at irrational roots are reported as certified
   `t`-intervals plus a decimal approximation of `y`.

## Layout

- `src/fraction.ts` — exact rational arithmetic (`BigInt`)
- `src/polynomial.ts` — integer polynomials: arithmetic, rational root theorem,
  Sturm sequences, root isolation
- `src/bezier.ts` — builds `N`, `D`, `P = N'D - ND'`, full extremum analysis
- `src/cli.ts` — runnable demo (5 examples)
- `test/` — Node built-in test runner (`node:test`), 22 tests

## Run

```bash
npm run sample   # demo: 5 worked examples
npm test         # node --test test/*.test.ts
```

## Sample output (`npm run sample`)

```
========================================================================
Example 1: quadratic rational Bezier, weights (1, 2, 1)
  control points y = [0, 1, 0]
  weights        w = [1, 2, 1]
  N(t) = - 4*t^2 + 4*t
  D(t) = - 2*t^2 + 2*t + 1
  P(t) = N'D - ND' = - 2*t + 1
  monotonicity:
    [0 , 1/2]  increasing
    [1/2 , 1]  decreasing
  critical points:
    max        t = 1/2 (exact)   y = 2/3 (exact)   [multiplicity 1, sign change: true]
  global max: t = 1/2,  y = 2/3 (exact)
  global min: t = 0,  y = 0 (exact)
========================================================================
Example 2: quadratic rational Bezier, weights (1, 2, 3) -> irrational root
  control points y = [0, 1, 0]
  weights        w = [1, 2, 3]
  N(t) = - 4*t^2 + 4*t
  D(t) = 2*t + 1
  P(t) = N'D - ND' = - 2*t^2 - 2*t + 1
  monotonicity:
    [0 , (412107968022933/1125899906842624 , 206053984011467/562949953421312)]  increasing
    [(412107968022933/1125899906842624 , 206053984011467/562949953421312) , 1]  decreasing
  critical points:
    max        t in (412107968022933/1125899906842624 , 206053984011467/562949953421312)  ~= 0.3660254037844388   y ~= 0.5358983848622454   [multiplicity 1, sign change: true]
  global max: t = (412107968022933/1125899906842624 , 206053984011467/562949953421312),  y = ~0.5358983848622454
  global min: t = 0,  y = 0 (exact)
========================================================================
Example 3: fractional control points (1/2, -3/4, 2), weights (2, 1/2, 3)
  control points y = [1/2, -3/4, 2]
  weights        w = [2, 1/2, 3]
  N(t) = 31*t^2 - 11*t + 4
  D(t) = 16*t^2 - 12*t + 8
  P(t) = N'D - ND' = - 49*t^2 + 92*t - 10
  monotonicity:
    [0 , (130427684779743/1125899906842624 , 4075865149367/35184372088832)]  decreasing
    [(130427684779743/1125899906842624 , 4075865149367/35184372088832) , 1]  increasing
  critical points:
    min        t in (130427684779743/1125899906842624 , 4075865149367/35184372088832)  ~= 0.11584305495281866   y ~= 0.46035455853669777   [multiplicity 1, sign change: true]
  global max: t = 1,  y = 2 (exact)
  global min: t = (130427684779743/1125899906842624 , 4075865149367/35184372088832),  y = ~0.46035455853669777
========================================================================
Example 4: cubic Bezier = (2t-1)^3, double stationary point at t = 1/2
  control points y = [-1, 1, -1, 1]
  weights        w = [1, 1, 1, 1]
  N(t) = 8*t^3 - 12*t^2 + 6*t - 1
  D(t) = 1
  P(t) = N'D - ND' = 4*t^2 - 4*t + 1
  monotonicity:
    [0 , 1/2]  increasing
    [1/2 , 1]  increasing
  critical points:
    stationary t = 1/2 (exact)   y = 0 (exact)   [multiplicity 2, sign change: false]
  global max: t = 1,  y = 1 (exact)
  global min: t = 0,  y = -1 (exact)
========================================================================
Example 5: cubic Bezier = 3t^2 - 2t^3, stationary points at endpoints
  control points y = [0, 0, 1, 1]
  weights        w = [1, 1, 1, 1]
  N(t) = - 2*t^3 + 3*t^2
  D(t) = 1
  P(t) = N'D - ND' = - 6*t^2 + 6*t
  monotonicity:
    [0 , 1]  increasing
  critical points:
    stationary t = 0 (exact)   y = 0 (exact)   [multiplicity 1, sign change: false]
    stationary t = 1 (exact)   y = 1 (exact)   [multiplicity 1, sign change: false]
  global max: t = 1,  y = 1 (exact)
  global min: t = 0,  y = 0 (exact)
```

## Test result (`npm test`)

```
tests 22
pass 22
fail 0
```
