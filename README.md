# exactsets

Exact univariate rational-constraint set computation in pure Python 3.11
(standard library only — no symbolic-algebra dependencies, no floating point
in any decision procedure).

## Features

- **Constraints**: comparisons of rational-coefficient rational functions
  against `< <= = != >= >`, combined with `&` (AND), `|` (OR), `~` (NOT).
- **Domain semantics**: the real zeros of *every original denominator* are
  excluded from the common domain first; cancellation never restores them and
  NOT never re-admits them. A zero-polynomial denominator empties the domain.
- **Canonical output**: disjoint open/closed intervals and isolated points.
  Endpoints may be irrational algebraic numbers, printed as
  `RootOf(square-free poly, root index, rational isolating interval)`.
- **Exact algebraic engine**: Sturm root counting, bisection isolation with
  rational-root extraction, root equality across different polynomials via
  GCD + Sturm evidence (never interval overlap alone), exact ordering by
  interval refinement, sign determination at algebraic points via gcd/Sturm.
  Root multiplicity (Yun square-free factorization) decides sign changes;
  isolated roots coming from equalities are kept.
- **Reviewable derivation**: `Solution.steps` records the denominator
  exclusion set, the ordered distinct critical roots, per-cell signs and
  per-point membership.
- **Certificate verifier**: `verify(cert)` recomputes polynomial identities,
  Sturm counts, interval isolation, every cell/point sign, multiplicities and
  the final set from the polynomial data alone — it never trusts the
  generator's "positive/negative" labels.
- **Budgets**: coefficient bit-length and refinement-step budgets.  On
  exhaustion the solver returns `status="partial"` with certified roots and
  pending isolating regions; it never claims a complete solution set.
- **Set algebra**: union / intersection / difference / complement of solution
  sets, and rational affine substitutions `x = a*t + b` with canonical
  endpoints.
- **Cross-checks**: an independent direct evaluator (`exactsets.evaluator`)
  evaluates the original expression at rational sample points with plain
  `Fraction` arithmetic.  Sampling is a sanity check, not a completeness
  proof; irrational boundaries are covered by the certificate verifier.

## Usage

```python
from exactsets import Rat, solve, verify

x2m2 = [-2, 0, 1]                      # x^2 - 2 (coefficients, low degree first)

sol = solve(Rat(x2m2, x2m2) >= 0)      # (x^2-2)/(x^2-2) >= 0
print(sol.set.canonical_str())
# (-∞, RootOf(x^2 - 2, #0, ...)) ∪ (RootOf(...#0...), RootOf(...#1...)) ∪ (RootOf(...#1...), +∞)

verify(sol.certificate)                # independent re-verification -> True

solve(Rat([-1, 1]).ne(0)).set          # x - 1 != 0  ->  (-∞, 1) ∪ (1, +∞)
~(Rat([1], [-1, 1]) > 0)               # NOT(1/(x-1) > 0), solved by solve(...)

s = solve(Rat(x2m2) <= 0).set          # [-√2, √2]
s.affine(2, 1)                         # substitute x = 2t + 1
s.intersection(solve(Rat([-1, 0, 1]) >= 0).set)
```

## Layout

- `exactsets/polys.py` — rational polynomial arithmetic, Sturm sequences,
  square-free factorization, root bounds.
- `exactsets/algebraic.py` — algebraic numbers, isolation, exact
  equality/ordering/sign, canonical root form.
- `exactsets/sets.py` — canonical `RealSet`, boolean ops, affine substitution.
- `exactsets/expr.py` — `Rat` builder, atoms, boolean expression trees.
- `exactsets/solver.py` — the solver and reviewable `SolveSteps`.
- `exactsets/certificate.py` — certificate format and independent verifier.
- `exactsets/evaluator.py` — independent direct evaluator for cross-checks.
- `exactsets/budget.py` — arithmetic/refinement budgets.
- `examples/demo.py` — runnable tour (`python3 examples/demo.py`).
- `tests/test_exactsets.py` — unittest suite.

## Tests

```
python3 -m unittest discover -s tests -t . -v
```
