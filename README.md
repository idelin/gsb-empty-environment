# ratbez — certified extrema of 1D rational Bezier curves

Computes the global maximum and minimum of a rational Bezier curve

    R(t) = ( sum_i w_i p_i B_i^n(t) ) / ( sum_i w_i B_i^n(t) ),   t in [0,1]

with rational control values `p_i` and positive rational weights `w_i`
(degree 1..5), using **only exact rational arithmetic** (Go standard library
`math/big`). No floating point is used anywhere.

## Method

- `R'(t) = M(t)/D(t)^2` with an integer-coefficient numerator polynomial `M`
  (after reducing `N,D` by their polynomial gcd).
- All distinct real roots of `M` in `(0,1)` are isolated by **Sturm
  sequences** + rational bisection into disjoint rational intervals
  (`Msf = squarefree(M)` handles repeated stationary points). Dyadic roots
  such as `t = 1/2` come out as exact points.
- Monotonicity on each segment between consecutive critical points is the
  exact sign of `M` at a rational sample point (the denominator is certified
  positive on `[0,1]` by a Sturm count).
- Candidates for extrema: the two endpoints (exact rational values) and every
  interior stationary point (algebraic value, enclosed by rational interval
  arithmetic, refined to width `<= eps`).
- **Ties are decided exactly**, never by floating-point luck:
  - endpoint vs endpoint: exact rational comparison;
  - endpoint vs interior: `R(alpha) = v` iff `gcd(Msf, N1 - v*D1)` vanishes
    at `alpha` (Sturm count on the isolating interval);
  - interior vs interior: both values are roots of the **value polynomial**
    `S(y) = Res_t(Msf, N1 - y*D1)` (computed by exact interpolation of
    Sylvester resultants); they are equal iff they fall into the same
    isolating interval of `S`. A rational stationary value that is a dyadic
    root of `S` is certified exactly via the gcd test.
- Constant curves (`M == 0`) return the whole interval `[0,1]` as the
  extremum locus.

## Certificate and checker

`Analyze` emits a `Cert`; `Verify` re-checks it deterministically (no search):
root count completeness (Sturm), disjoint ordered isolating intervals each
containing exactly one root, denominator positivity, monotonicity signs,
reliability of every value enclosure, exactness of every claimed tie,
disjointness of distinct groups, max/min dominance, and enclosure width
`<= eps`. `SampleCheck` evaluates the curve exactly at uniform sample points
as an **auxiliary** sanity check only — it is not part of the proof.

## Usage

    go test ./...          # run the test suite
    go build ./cmd/ratbez
    ./ratbez -p "0,1,0" -w "1,1,1" -eps "1/1000" [-samples 1000] [-json]

`-p` control values, `-w` positive weights (comma-separated rationals),
`-eps` positive rational tolerance for the extremum enclosures.
