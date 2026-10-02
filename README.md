# rational-simplex

Exact rational-arithmetic simplex solver in TypeScript. All tableau entries
are `Fraction`s backed by `BigInt`, so every basis exchange (pivot) is exact —
no floating-point loss can ever alter a pivot decision or the optimum.

## Features

- **Exact arithmetic**: `Fraction` keeps a normalized `BigInt` numerator /
  denominator pair; `1/3 + 1/3 + 1/3 === 1` exactly.
- **Two-phase primal simplex** with Bland's rule (guaranteed termination,
  even on degenerate/cycling problems such as Beale's example).
- **Model**: `max|min c^T x` subject to rows `A_i x {<=, >=, =} b_i`, `x >= 0`.
- **Certificates**:
  - optimal → exact primal vector, objective value, and dual prices
    `y = c_B^T B^{-1}` (strong duality `y^T b = c^T x` proves optimality);
  - unbounded → feasible point + recession ray with `c^T d > 0`;
  - infeasible → Farkas vector `y` with `y^T A >= 0` but `y^T b < 0`.

## Requirements

Node.js >= 23.6 (runs TypeScript natively via type stripping; no build step,
no dependencies). An `.nvmrc` is provided.

## Usage

```ts
import { solveLP } from "./src/simplex.ts";

const result = solveLP({
  objective: "max",
  coefficients: [3, 4],                 // max 3 x1 + 4 x2
  constraints: [
    { coefficients: [2, 1], sense: "<=", rhs: 2 },
    { coefficients: [1, 3], sense: "<=", rhs: "9/4" },
    { coefficients: [1, 1], sense: "<=", rhs: 2 },
  ],
});
// result.status        === "optimal"
// result.primal        === [3/4, 1/2]   (exact Fractions)
// result.objectiveValue === 17/4        (exact Fraction)
// result.duals         === [1, 1, 0]    (shadow prices)
```

Coefficients accept integers, `bigint`, `"p/q"` fraction strings, and finite
decimal strings — all parsed exactly. Plain `number` input must be a safe
integer so no binary float rounding can enter the model.

## Run the sample

```console
$ npm run sample

=== 1. Maximize with 3 inequality constraints (exact rational simplex)
status          : optimal
pivot iterations: 2
optimal x       : (3/4, 1/2)
maximum         : 17/4  (exactly 17/4)
dual prices y   : (1, 1, 0)

dual price proof:
  (y^T A)_1 = 3 >= c_1 = 3  (dual feasible)
  (y^T A)_2 = 4 >= c_2 = 4  (dual feasible)
  y >= 0 componentwise: true
  y^T b = 17/4 = optimal value  (strong duality => x is optimal)

=== 2. Unbounded problem (ray certificate) ======================
status: unbounded
feasible point: (1, 0)
ray direction : (1, 1)
x(t) = point + t * ray stays feasible, objective -> +infinity

=== 3. Infeasible problem (Farkas certificate) ==================
status: infeasible
Farkas y: (1, -1)
y^T b = -2 < 0 while y^T A >= 0  => no feasible x
```

## Run the tests

Uses the Node.js built-in test runner (`node:test`), no dependencies:

```console
$ npm test
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

Test coverage:

- `test/fraction.test.ts` — normalization, exact parsing, exact arithmetic,
  comparisons, beyond-float64 precision.
- `test/simplex.test.ts` — the acceptance LP (3 inequality constraints,
  exact maximum `17/4` with dual-price proof), unbounded ray certificate,
  Farkas infeasibility certificate, equality constraints, minimization,
  negative RHS normalization, and Beale's degenerate cycling example.

## Layout

- `src/fraction.ts` — exact rational number type (`BigInt`-backed).
- `src/simplex.ts` — two-phase simplex, dual extraction via `B^{-1}`
  tracking, certificate generation.
- `examples/sample.ts` — runnable demo (`npm run sample`).
- `test/` — `node:test` suites (`npm test`).
