# rootfind — bounded exact root isolation for rational polynomials

Finds **all** real roots of a univariate polynomial with rational
coefficients in a closed interval `[a, b]`, to a precision given as a
rational number. Reports each root as an isolating interval together with
its exact multiplicity, and reports unfinished intervals when the
bisection budget runs out.

## Why not sampling?

A root of even multiplicity (e.g. `(x - 1/3)^2`) produces **no sign
change**, so no amount of point sampling can ever detect it, let alone
prove that all roots were found. This package never uses sampling or
sign-change heuristics for completeness. Instead:

- **Exact arithmetic** — everything is `math/big.Rat`; there is no
  floating-point error anywhere.
- **Sturm sequences** — for a square-free polynomial, `V(a) - V(b)`
  (sign variations of the Sturm chain) is the *exact* number of distinct
  real roots in `(a, b]`. Every work-queue interval carries this exact
  count; an interval is discarded only when the count is 0, so no root —
  including even-multiplicity ones — can be missed.
- **Yun's square-free factorization** — `p = c · ∏ fᵢ^i` with `fᵢ`
  square-free and pairwise coprime gives each root's multiplicity exactly;
  the square-free part `p / gcd(p, p′)` feeds the Sturm machinery.
- **Bisection with budget** — intervals are halved until each contains
  exactly one distinct root and its width is `<= prec`. Roots landing on
  dyadic subdivision points are reported exactly. If the bisection budget
  is exhausted, remaining intervals (with their exact root counts) are
  returned in `Result.Unfinished` and `Result.Complete()` is false.

## Usage

```go
p := rootfind.Linear(rootfind.Rat(1, 3)).Pow(2).Mul(rootfind.Linear(rootfind.Rat(2, 1)))
res := rootfind.Isolate(p, rootfind.Rat(0, 1), rootfind.Rat(3, 1), rootfind.Rat(1, 1000), -1)
for _, r := range res.Roots {
    fmt.Println(r) // isolating interval or exact value + multiplicity
}
```

`Isolate(p, a, b, prec, budget)`: `prec` is a positive rational bound on
isolating-interval width; `budget` bounds the number of bisections
(negative = unlimited).

## Demo and tests

```
go run ./cmd/rootfind   # demo incl. the sampling pitfall
go test ./...           # square-factor examples, clustered roots, budget
```
