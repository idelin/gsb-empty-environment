# movint

Go library that computes, **exactly**, the closed time intervals during which
pairs of linearly moving 1-D closed intervals intersect.

- Endpoints move linearly with **integer velocities**: `p(t) = P + V·t`.
- Endpoints **may cross**; the interval at time `t` is always
  `[min(p0,p1), max(p0,p1)]`.
- Time is restricted to a **rational domain** `[0, T]`.
- All arithmetic uses `math/big.Rat` — results are exact, with no
  floating-point error.
- **Instantaneous contacts are kept**: a touch at a single time `t` is
  returned as the degenerate closed interval `[t, t]`.

## Usage

```go
a := movint.MovingInterval{P0: 0, V0: 0, P1: 1, V1: 0}    // static [0,1]
b := movint.MovingInterval{P0: -10, V0: 2, P1: 20, V1: -1} // endpoints cross at t=10
T := big.NewRat(25, 1)

times := movint.IntersectionTimes(a, b, T)
// => [[0, 11/2], [19, 25]]  (two disjoint closed intervals)

// Batch over two groups of intervals (every A_i x B_j pair):
results := movint.IntersectAll(groupA, groupB, T)

// Sample-based verification of a result:
err := movint.Verify(a, b, T, times)
```

## Method

Intervals `A` and `B` intersect at time `t` iff

```
hiA(t) >= loB(t)   and   hiB(t) >= loA(t).
```

`hiA - loB` is a convex piecewise-linear function (max of linears minus min
of linears) whose only breakpoints are the two endpoint-crossing times. The
algorithm:

1. Collects breakpoints `{0, T, tA, tB}` and evaluates both functions there.
2. On each segment solves one linear inequality with an exact rational root,
   yielding the closed sets `{hiA >= loB}` and `{hiB >= loA}`.
3. Intersects the two closed sets — at most three disjoint closed intervals
   inside `[0, T]`.

`Verify` cross-checks a result by sampling an interior point of every
returned interval (must intersect) and an exterior point of every gap in
`[0, T]` (must not intersect).

## Test

```
go test ./...
```

Hand-computed cases (touches at `0`/`T`, endpoint crossings, degenerate
`T = 0`, fractional roots) plus 500 randomized pairs cross-checked against
dense rational-grid sampling.
