# exact-rational

Exact rational arithmetic in TypeScript, built entirely on **BigInt**. Values
never pass through `number`, so there is no silent precision loss on large
inputs, and no truncated decimal is ever treated as exact.

Requires Node.js >= 22.6 (tests and examples run on the built-in TypeScript
type stripping and the built-in test runner — no dependencies).

## What it provides

- **Canonical fractions** (`src/fraction.ts`)
  - reduced by BigInt gcd; a negative denominator moves to the numerator;
    every zero is exactly `0/1`
  - exact `+ - * /`, negation, reciprocal
  - comparison by **cross multiplication** (`a.n*b.d` vs `b.n*a.d`) — safe for
    arbitrarily large numerators/denominators, no decimal expansion
  - explicit decimal rounding: `floor`, `ceil`, `trunc`, `half-up`; the result
    stays an exact `Fraction`, the rule is always named
- **Rational → decimal expansion** (`src/decimal.ts`)
  - `1/6 → 0.1(6)`, `1/7 → 0.(142857)`, `1/28 → 0.03(571428)`
  - preperiod located by **number theory** (strip factors 2 and 5 from the
    reduced denominator; length is `max(v2, v5)`)
  - period located by a bounded **remainder-state table** (a repeated
    remainder marks the cycle); the table and the digit output are both capped
    by an explicit budget
  - over budget → result is marked `kind: "incomplete"` and rendered with a
    trailing `...`; it never hangs and never claims a false exact period
  - `locateCycle` (prefix length / period length) and `pageDigits` (paged
    fractional digits; a known period pages forever by index arithmetic, so
    deep pages do not materialize giant strings)
  - `parseExact` reconstructs exact rationals from `0.1(6)` style text and
    **rejects** truncated input such as `0.333...`
- **Lazy-reduction expression DAG** (`src/expr.ts`)
  - leaves hold raw `(n, d)` pairs; internal nodes combine raw products and do
    **not** cancel factors while the graph is built/traversed — reduction is a
    single gcd performed lazily at the root on `evaluate`
  - every evaluation runs under a **bit-length budget** (`maxBits` of any
    intermediate numerator/denominator); exceeded nodes are reported as
    `incomplete` with the offending node ids instead of blowing up memory
  - `compare` decides order via one cross product, without expanding huge
    decimal strings; shared subexpressions are memoized (DAG, not a tree)

## Run

```sh
npm test     # node --test over the four test files (38 tests)
npm run demo # prints the worked examples
```

## Example

```ts
import { add, frac, toFractionString } from "./src/fraction.ts";
import { expand, format, pageDigits } from "./src/decimal.ts";
import { RationalDAG } from "./src/expr.ts";

format(expand(frac(1n, 6n)));                 // "0.1(6)"
pageDigits(frac(1n, 7n), 1_000_000, 7);       // digits deep in the period

const dag = new RationalDAG();
const a = dag.frac(frac(1n, 6n));
const b = dag.frac(frac(1n, 7n));
dag.compare(a, b);                            // { result: 1, status: "ok" }
toFractionString(dag.evaluate(dag.add(a, b)).value!); // "13/42"
dag.evaluate(dag.leaf(2n ** 200n), 64).status;        // "incomplete"
```

## Testing notes

- `tests/fraction.test.ts`, `tests/decimal.test.ts`, `tests/expr.test.ts`:
  canonicalization, the four operations, cross-product comparison, all
  rounding modes, repeating/terminating/incomplete expansions, paging,
  parsing, DAG budgets and division-by-zero.
- `tests/exhaustive.test.ts`: an independent small-integer sweep (numerators
  and denominators over a signed grid) that re-checks canonicalization, the
  four-operation identities and comparison with **separate reference
  formulas**, and verifies every exact `pre(period)` rendering against an
  independent long division and through an exact parse round-trip. Truncated
  prefixes are explicitly asserted *not* to parse as exact values
  (e.g. `1/3 ≠ 333/1000` even though both begin `0.333`).
