# certitrig

Certified trigonometric evaluation over the rationals (Go, `math/big` only).

`SinCos(x, eps, budget)` takes a rational angle `x` (radians) and returns
rational intervals that are **guaranteed** to contain `sin(x)` and `cos(x)`.
If `eps > 0`, refinement stops once both interval widths are at most `eps`.
No floating-point constants or arithmetic are used anywhere: every step is
exact rational arithmetic with certified remainder bounds.

## How correctness is enforced

- **Interval arithmetic**: all operations on `Interval` (`interval.go`)
  conservatively enclose the exact result; successive valid enclosures of
  the same value are intersected, so results only ever get narrower.
- **Certified pi**: `PiInterval` (`pi.go`) computes pi via Machin's formula
  `pi = 16*arctan(1/5) - 4*arctan(1/239)`; each `arctan` partial sum carries
  the alternating-series remainder bound, so the pi interval used for range
  reduction is itself a certified enclosure — never a float constant.
- **Range reduction**: the quadrant `q = round(x / (pi/2))` is chosen with
  exact rational arithmetic against the certified midpoint (any `q` yields a
  valid enclosure; the nearest one keeps the residual small). The residual
  `t = x - q*(pi/2)` is computed in interval arithmetic with the certified
  pi/2 interval, so it encloses the true residual. pi/2 precision is sized
  as `eps / (16|q| + 64)` so the residual width stays well below `eps`.
- **Series with remainder bounds** (`trig.go`): sin/cos Taylor partial sums
  over the residual interval are augmented with exact rational Lagrange
  remainder bounds (`|R_sin| <= M^(2n+3)/(2n+3)!`, `|R_cos| <= M^(2n+2)/(2n+2)!`,
  `M = max|t|`), giving certified enclosures at every truncation point.
- **Budget**: `Budget` caps the number of series terms evaluated. On
  exhaustion the current enclosure is returned; the initial enclosure is
  `[-1, 1]`, so the result is a legal enclosure at every budget level.

## Auxiliary checks (tests only)

`trig_test.go` validates zeros, angles near pi/2, huge angles
(`10^18`, `10^30 + 7`, `10^24`, rationals with 30-digit numerators),
budget exhaustion legality, and quadrant mapping. Independent identities
(`sin^2 + cos^2` enclosing 1) and containment against `math.Sin`/`math.Cos`
references are used only as auxiliary sanity checks — the guarantee itself
is constructive.

## Usage

```go
x, _ := new(big.Rat).SetString("355/113")
eps := new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(1000000)) // 1e-6
sinI, cosI := certitrig.SinCos(x, eps, &certitrig.Budget{Left: 10000})
```

Run tests with:

```sh
GOCACHE=/tmp/gocache go test ./...
```
