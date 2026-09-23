# Exact rational polynomial GCD toolkit (Python 3.11, stdlib only)

Univariate polynomial division with remainder, extended Euclid with
Bezout coefficients, and a subresultant polynomial remainder sequence
(PRS) with coefficient growth control — all over the rationals with
exact `fractions.Fraction` arithmetic. No symbolic algebra libraries.
Tests use `unittest` only.

## Files

- `polygcd.py` — core arithmetic, `divmod_poly` (quotient/remainder,
  zero divisor rejected, deg r < deg g), `xgcd` (monic gcd + Bezout
  coefficients + per-step domain-annotated log).
- `prs.py` — `pseudo_divmod`, `PRSComputation` (subresultant PRS with
  beta-factor growth control, per-step quotient/remainder and Bezout
  certificates, pause/resume, JSON snapshots, coefficient budget),
  `subresultant_gcd`, `CoefficientBudget`, `BudgetExceeded`.
- `reference.py` — independent naive schoolbook reference used only for
  validation: division identity, degree descent, monic normalization,
  and gcd agreement. GCD correctness is established by exact Bezout
  identities plus an independent Euclidean computation — never by a
  divisibility-only argument.
- `test_polygcd.py` — 20 unittest cases (randomized + edge cases).
- `demo.py` — runnable samples.

## Conventions

- Polynomials are little-endian lists of `Fraction`: `p[i]` is the
  coefficient of `x**i`; the zero polynomial is `[]`; `deg(0) = -1`.
- `gcd(0, 0) = 0`, `gcd(f, 0) = monic(f)`, `gcd(0, g) = monic(g)`.
- GCD results are always monic; Bezout identity `s*f + t*g == d` holds
  exactly.
- Every logged transformation states the domain it is valid in
  (e.g. exact division in `Q[x]` since Q is a field; pseudo-division in
  the integral domain `Q[x]`; exact scalar division by beta in Q).

## Budget, pause/resume

`PRSComputation(f, g, budget=CoefficientBudget(max_digits))` is a state
machine: `pending -> running -> {paused, budget_exceeded, done}`.
`run(max_steps=n)` pauses after n steps; `resume()` continues;
`snapshot()` / `from_snapshot()` serialize the exact state (JSON-safe).
Exceeding the coefficient budget leaves the computation unfinished
(`budget_exceeded`, `result is None`); resume with a larger budget to
finish.

## Run

```
python3.11 demo.py
python3.11 -m unittest -v
```

## Sample output (abridged)

```
1. Division with remainder in Q[x] (exact rational arithmetic)
f = 3*x^3 - x^2 + 2*x - 5
g = 2*x - 1
q = 3/2*x^2 + 1/4*x + 9/8
r = -31/8   (deg r = 0 < deg g = 1)
identity f = q*g + r verified by independent naive reference: OK
division by zero polynomial rejected: polynomial division by the zero polynomial

3. Subresultant PRS: f = 6(x-1)^3(x+2)^2, g = 4(x-1)^2(x+2)^3
step -> R2:
  pseudo-quotient  Q = 6
  pseudo-remainder R = -72*x^4 - 144*x^3 + 216*x^2 + 288*x - 288   (mult = 4)
  beta (growth control) = -1
  next remainder R/beta = 72*x^4 + 144*x^3 - 216*x^2 - 288*x + 288
  Bezout certificate: s = -4, t = 6
gcd = x^4 + 2*x^3 - 3*x^2 - 4*x + 4   (monic; = (x-1)^2(x+2)^2)
Bezout: s = -1/18, t = 1/12

4. Coefficient budget
tiny budget (2 digits): status = budget_exceeded, result = None
resumed with larger budget: status = done, gcd = 1

ALL DEMO CHECKS PASSED
```

## Test results

```
$ python3.11 -m unittest -v
test_budget_exceeded_keeps_unfinished ... ok
test_pause_resume_equivalence ... ok
test_snapshot_is_json_serializable ... ok
test_snapshot_restore ... ok
test_subresultant_gcd_raises_on_budget ... ok
test_identity_random ... ok
test_pseudo_divmod_identity_random ... ok
test_remainder_degree_below_divisor ... ok
test_zero_dividend ... ok
test_zero_divisor_rejected ... ok
test_constant_gcd_normalized_to_one ... ok
test_growth_control_keeps_integer_coeffs ... ok
test_matches_independent_naive_gcd_random ... ok
test_multiple_common_factors ... ok
test_step_identities_descent_and_certificates ... ok
test_zero_inputs ... ok
test_bezout_random ... ok
test_nonmonic_inputs ... ok
test_steps_document_domains ... ok
test_zero_boundaries ... ok
----------------------------------------------------------------------
Ran 20 tests in 0.212s

OK
```
