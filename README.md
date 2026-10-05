# trigcert

Certified rational enclosures of `sin` and `cos` for rational angles, in Go.

```go
sinI, cosI := trigcert.SinCos(x, eps, budget) // x, eps *big.Rat
```

Returns intervals with rational endpoints **guaranteed** to contain
`sin(x)` and `cos(x)`. When the budget suffices, each interval has width
at most `eps`.

## Why the guarantee holds

Only exact rational arithmetic (`math/big.Rat`) and analytic remainder
bounds are used. No floating-point constant ever enters the computation.

- **Certified pi.** `PiInterval(n)` computes pi from Machin's formula
  `pi = 16*atan(1/5) - 4*atan(1/239)`; each alternating arctan series is
  truncated with the first omitted term as a rigorous remainder bound.
- **Certified range reduction.** `x - 2k*pi` is evaluated in interval
  arithmetic with the certified pi interval, so the reduced argument is
  an interval whose width is fully accounted for. pi is refined until
  the reduction width is below `eps/8` (or the budget runs out).
- **Series with remainder bounds.** At the rational midpoint, sin/cos
  are summed by Taylor series with the Lagrange bound `|m|^n/n!`
  (all derivatives are bounded by 1), i.e. the first omitted term.
- **Width propagation.** The reduction half-width is added via the
  1-Lipschitz property of sin/cos; results are clipped to `[-1, 1]`.

## Budget semantics

`budget` bounds the total number of series terms (pi refinement plus
Taylor terms). Every intermediate state is already a valid enclosure, so
exhausting the budget never breaks the guarantee — the current, possibly
wider, enclosure is returned. `budget <= 0` yields the trivially valid
`[-1, 1]`.

## Tests

`go test ./...` covers: certified pi (window overlap, nesting, exclusion
of 22/7), exact zeros, angles within ~1e-56 of pi/2, huge angles
(~1e7 and ~1e13, positive and negative), budget exhaustion, and — as
auxiliary checks only — the Pythagorean identity, double-angle and
addition formulas, interval containment, and an independent reference
Taylor implementation.
