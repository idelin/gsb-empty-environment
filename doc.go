// Package trigcert computes certified rational enclosures of sin and cos
// for rational arguments.
//
// Guarantee: for a rational angle x and positive rational tolerance eps,
// SinCos returns intervals with rational endpoints that are mathematically
// guaranteed to contain sin(x) and cos(x). The guarantee rests only on
// exact rational arithmetic (math/big.Rat) and analytic remainder bounds:
//
//   - pi is obtained as a certified interval from Machin's formula
//     pi = 16*atan(1/5) - 4*atan(1/239), where each alternating arctan
//     series is truncated with the first omitted term as remainder bound.
//     No floating-point constant ever enters the computation.
//   - Range reduction x - k*pi is performed in interval arithmetic with
//     that certified pi interval, so the reduced argument is itself an
//     interval whose width is accounted for.
//   - sin/cos at the rational midpoint are evaluated by Taylor series
//     with a Lagrange remainder bound |m|^n/n! (all derivatives of sin
//     and cos are bounded by 1 in absolute value).
//   - The reduction half-width is propagated via the Lipschitz constant
//     1 of sin and cos, and results are clipped to [-1, 1].
//
// The budget parameter bounds the total number of series terms used.
// Every intermediate state of the computation is already a valid
// enclosure, so when the budget is exhausted before eps is met, the
// functions return the current (possibly wider than eps) valid enclosure.
// With budget <= 0 the trivially valid enclosure [-1, 1] is returned.
package trigcert
