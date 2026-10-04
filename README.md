# piecewise

Exact arithmetic on piecewise-polynomial functions with rational
breakpoints (Go, `math/big.Rat` throughout — no floating point).

## Model

- A `Function` is a sorted list of `Segment`s; each segment carries a
  `Poly` valid on its open interval `(Lo, Hi)`.
- `LoVal`/`HiVal` optionally override the point values at the endpoints,
  so left and right values at a shared breakpoint may differ.
- Point values never contribute to integrals; impulse / distributional
  terms are out of scope.

## API

- `New()` — empty function.
- `f.Replace(lo, hi, p, loVal, hiVal)` — returns a **new** version with
  `[lo, hi)` replaced by polynomial `p`; the receiver is unchanged, so old
  versions stay valid. Errors with `ErrEmptyInterval` when `lo >= hi`.
- `f.Integrate(a, b)` — exact integral, summed per segment so endpoints
  are never double counted. Reversed bounds negate; `a == b` gives zero.
- `f.ValueAt(x)` — point value (right segment wins at a shared breakpoint).
- `f.DerivativeAt(x)` — one-sided derivatives, continuity, and whether the
  two-sided derivative exists (handles jumps, kinks, smooth joins).
- `f.Deriv()` — piecewise derivative as a new `Function`.

## Test

```sh
go test ./...
```
