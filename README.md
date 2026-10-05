# rootfind

Bounded real-root isolation for rational polynomials in Go.

Given a polynomial with rational coefficients, a closed interval `[lo, hi]`,
and a rational precision `eps`, `FindRoots` returns **every** real root in
the interval as a certified isolating interval of width at most `eps`,
together with the root's multiplicity.

## Why sampling is not enough

A root of even multiplicity (e.g. `(x-2)^2`) never changes the sign of the
polynomial, so any method that infers completeness from sign changes of
sampled points can silently miss it. This package never samples for
completeness:

1. **Square-free factorization** (Yun's algorithm, exact rational GCDs)
   splits `p` into factors `Q_m` whose roots are exactly the roots of `p`
   of multiplicity `m`.
2. **Sturm sequences** give the *exact* number of distinct real roots of
   each `Q_m` in any rational interval `(a, b]` — including roots with no
   sign change.
3. **Bisection with exact counts** subdivides intervals whose Sturm count
   exceeds 1, and shrinks single-root intervals to width `<= eps`. Roots
   hit exactly by a rational midpoint are certified as point intervals.

All arithmetic uses `math/big.Rat`, so root counts and isolating intervals
are exact, not probabilistic.

## Budget

`FindRoots(p, lo, hi, eps, budget)` caps the total number of bisection
steps (`budget < 0` means unlimited). If the budget runs out, the result
is marked incomplete: `Result.Incomplete` lists every unresolved interval
together with a certified lower bound on how many roots it still contains,
so no root is ever silently dropped.

## Usage

```go
p := rootfind.PolyFromStrings("2", "-3", "1") // x^2 - 3x + 2
res, err := rootfind.FindRoots(p, rat("0"), rat("5"), rat("1/1000"), -1)
for _, r := range res.Roots {
    fmt.Println(r.Mult, r.Lo, r.Hi)
}
```

Run the demo (square-factor example plus budget-exhaustion report):

```
go run ./cmd/rootfind
```

Run the tests (square-factor constructions, even-multiplicity roots that
sampling cannot see, endpoint roots, budget exhaustion, sqrt(2)):

```
go test ./...
```
