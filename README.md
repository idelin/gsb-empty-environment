# exact-cmp

Resumable precision-refinement comparison over **integers, rationals, decimals
and explicit error intervals** — returning `lt` / `eq` / `gt` / `undetermined`
without ever routing values through binary floating point.

## Core ideas

- **Exact kernel** (`src/rational.ts`): every number is a reduced BigInt
  rational `num/den`. Decimals parse exactly (`0.1` is `1/10`, not a float);
  big integers never pass through `Number`, so `2^70` and `2^70+1` stay
  distinguishable. Decimal printouts require an explicit rounding mode
  (`floor` | `ceil` | `trunc` | `half-up` | `half-even`) — there is no default.
- **Affine uncertain values** (`src/value.ts`): `value = centre + Σ eᵢ` with
  `eᵢ ∈ [-radiusᵢ, radiusᵢ]`. Each error term carries a **provenance token**.
  Terms with the same token are the *same* error variable: they cancel in a
  difference instead of being summed as independent errors (with unequal
  radii, only the shared part cancels; the excess stays as residual).
- **Three-way decision rule**: with `d = centreA − centreB` and `R` the
  residual (non-shared) radius sum:
  `d > R → gt`, `d < −R → lt`, `d = 0 ∧ R = 0 → eq`, otherwise
  **undetermined**. Overlapping intervals are never reported as equal, and no
  epsilon is ever used to merge close numbers.
- **Refinement protocol** (`src/session.ts`): operands are
  `EvidenceProvider`s whose evidence tightens per level. `CompareSession`
  starts at level 0; while evidence overlaps it spends **budget** to request
  finer evidence from the coarser side. A definite answer requires separated
  intervals or exact-equality evidence. `suspend()` serialises
  `{levels, budgetUsed, budget}` to JSON; `resume(json, providers, extraBudget)`
  continues exactly where the session stopped — verified to reach the same
  decision at the same refinement levels as an uninterrupted run.
- **Literal safety** (`src/parse.ts`): integers, decimals (incl. `1.5e-3`),
  fractions `p/q`, intervals `a±b` (provenance token required for
  correlation). `NaN` / `Infinity` are rejected as unsupported.

## Layout

- `src/rational.ts` — BigInt rational arithmetic, explicit rounding modes
- `src/value.ts` — affine values, provenance-aware three-way comparison
- `src/session.ts` — refinement protocol, budget, suspend/resume, providers
  (`exact`, `fixedInterval`, `halving`, `digitStream`)
- `src/parse.ts` — literal parsing; rejects `NaN`/`Infinity`
- `test/` — node:test suites (exact arithmetic, transitivity over a finite
  exact reference set, interval/provenance semantics, protocol resumability)
- `examples/demo.ts` — runnable tour of every behaviour above

## Requirements & usage

Node ≥ 22.6 (runs TypeScript via type stripping; developed on v23.9.0,
see `.nvmrc`). No dependencies.

```sh
npm test     # node --experimental-strip-types --test "test/*.test.ts"
npm run demo # node --experimental-strip-types examples/demo.ts
```

## Test results (Node v23.9.0)

```
ℹ tests 29
ℹ pass 29
ℹ fail 0
```

Highlights: total-order/antisymmetry/transitivity checked over all triples of
a 13-value exact reference set (including `2^70`, `2^70+1`, `1/3` vs 33-digit
decimal); `1` vs `1+10⁻³⁰` never merged; suspend/resume reaches the identical
decision at identical refinement levels as an uninterrupted run.
