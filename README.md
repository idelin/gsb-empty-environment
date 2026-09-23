# polyq — exact univariate polynomial algebra over Q

Python 3.11+, standard library only (`fractions.Fraction`, `unittest`).
No symbolic algebra libraries. All arithmetic is exact rational arithmetic.

## Layout

- `polyq/core.py` — polynomials as dense `list[Fraction]` (index = power,
  no trailing zeros, `[]` = zero polynomial with degree -1); `divmod_q`
  (exact division over a field) and `pseudo_divmod` (ring operations only,
  valid over any integral domain; returns the exact `lc(b)**k` factor).
- `polyq/euclid.py` — `extended_gcd(a, b) -> (g, s, t)` with `s*a + t*b == g`
  exactly and `g` monic; `PRSComputer`, a subresultant polynomial remainder
  sequence (Brown–Traub β-scaling for denominator/coefficient growth
  control) with per-step quotient/remainder, Bezout certificates, domain
  notes, pause/resume, JSON checkpoints, and a coefficient budget.
- `polyq/reference.py` — independent naive reference (sparse-dict
  representation, separate code path) plus checkers used by the tests.
- `tests/test_polyq.py` — 21 unittests.
- `demo.py` — runnable sample (output in `demo_output.txt`).

## Semantics and guarantees

- Remainder degree is always strictly below the divisor degree.
- Division (or pseudo-division) by the zero polynomial raises
  `ZeroDivisionError`.
- `gcd` is normalised monic; degenerate cases: `gcd(0,0)=0`,
  `gcd(a,0)=monic(a)`, a constant gcd normalises to `1`.
- Bezout identity `s*a + t*b == g` holds exactly (Fraction arithmetic).
- GCD maximality is certified by the exact Bezout identity (every common
  divisor of `a, b` divides `g`) together with `g | a`, `g | b` — never
  inferred from divisibility alone.
- Each PRS step records `pseudo_quotient`, `pseudo_remainder`, the
  subresultant factor `beta`, the scaled `remainder`, a Bezout certificate
  `(s, t)` against the original inputs, and a note stating the domain in
  which the transformation is valid.
- Budget: measured in total decimal digits of all live coefficients. On
  exceed the machine stops in `budget_exceeded` (unfinished, `gcd is None`)
  and resumes when the budget is raised. `run(max_steps=k)` pauses;
  `checkpoint()`/`restore()` give a JSON-serialisable snapshot.

## Run

```
python3 demo.py                          # sample run (demo_output.txt)
python3 -m unittest discover -s tests    # test suite
```

## Sample run (abridged)

```
1. Exact division with remainder over Q[x]
a = 3/4*x^3 + 2*x^2 - 1/2*x + 1
b = 2*x - 3
q = 3/8*x^2 + 25/16*x + 67/32
r = 233/32   (deg r = 0 < deg b = 1)
independent check: a == q*b + r  [OK]
division by zero polynomial rejected: polynomial division by the zero polynomial

2. Extended Euclidean algorithm (non-monic inputs, multiple common factor)
a = 4*x^4 + 2*x^3 - 22*x^2 + 22*x - 6
b = 3*x^4 + 9*x^3 - 21*x^2 - 45*x + 54
g = x^2 + 2*x - 3   (monic gcd)
s = 5/168*x + 3/28
t = -5/126*x - 11/252
independent check: s*a + t*b == g, g | a, g | b, g monic  [OK]

3. Subresultant PRS: per-step quotient/remainder + Bezout certificates
step 1:
  dividend        = 3*x^4 + 9*x^3 - 21*x^2 - 45*x + 54
  divisor         = 30*x^3 - 18*x^2 - 246*x + 234
  pseudo-quotient = 90*x + 324
  pseudo-rem      = 9072*x^2 + 18144*x - 27216
  beta            = -3
  remainder       = -3024*x^2 - 6048*x + 9072
  bezout s        = -90*x - 324
  bezout t        = 120*x + 132
  domain note     = pseudo-division by lc^(delta+1) with delta=1: ring
    operations only, valid over any integral domain (here Q[x]); division
    by subresultant factor beta=-3: exact over the field Q, controls
    coefficient/denominator growth
gcd from PRS = x^2 + 2*x - 3
independent check of every step + final gcd certificate  [OK]

4. Pause/resume and coefficient budget
tiny budget=120 -> status: budget_exceeded (gcd is None: True)
budget raised, resume -> status: completed
gcd = x^2 + 2*x - 3
pause every step: paused -> paused -> paused -> paused -> completed -> done
checkpoint JSON round-trip: 3023 bytes, restored gcd matches: True

5. Zero-polynomial boundaries
gcd(0, 0) = 0  [checked]
gcd(6*x^2 + 4*x + 2, 0) = x^2 + 2/3*x + 1/3  [checked]
gcd(0, -6*x^2 + 3) = x^2 - 1/2  [checked]
```

## Test results

```
$ python3 -m unittest discover -s tests -v
test_exact_division ... ok
test_rational_remainder ... ok
test_remainder_degree_strictly_smaller ... ok
test_division_by_zero_rejected ... ok
test_dividend_zero ... ok
test_pseudo_division_identity ... ok
test_basic ... ok
test_nonmonic_inputs ... ok
test_multiple_common_factors ... ok
test_coprime ... ok
test_zero_boundaries ... ok
test_constant_polynomials ... ok
test_bezout_exact_random ... ok
test_matches_extended_gcd ... ok
test_step_certificates_and_degree_descent ... ok
test_nonmonic_and_multiple_factors ... ok
test_pause_resume ... ok
test_checkpoint_restore ... ok
test_budget_exceeded_stays_unfinished_then_resumes ... ok
test_random_cross_check ... ok
test_growth_control_beats_naive_prs ... ok
----------------------------------------------------------------------
Ran 21 tests in 0.095s

OK
```
