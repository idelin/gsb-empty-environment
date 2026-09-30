# Randomized Response: Simulation and Aggregate Estimation

Pure Python 3.11+ (standard library only) implementation of binary and k-ary
randomized response, with joint aggregate estimation over heterogeneous
response channels. **Synthetic data only.** The statistical experiments in
this repository are illustrative and do **not** constitute a formal privacy
proof.

## Files

- `randomized_response.py` — channel definitions, parameter validation, simulation.
- `estimate.py` — grouped joint estimation, constraints, diagnostics, confidence intervals.
- `exact.py` — exact small-domain reference distribution of the inverse estimator.
- `linalg.py` — minimal dense linear algebra (RREF, null space, inverse, Jacobi eigenvalues).
- `demo.py` — runnable demonstration (`python3 demo.py`).
- `test_rr.py` — unit tests (`python3 -m unittest test_rr -v`).

## Definitions and parameters

- Alphabet size `k >= 1` (integer). Population distribution `pi`: probability
  vector of length `k`.
- Channel matrix `P` (k x k, row-stochastic, **known**): `P[i][j] = Pr[output=j | input=i]`.
- k-ary epsilon-channel: diagonal `e^eps / (e^eps + k - 1)`, off-diagonal
  `1 / (e^eps + k - 1)`, with `epsilon >= 0` finite.
- Binary channel: either `epsilon` (symmetric) or explicit stay-probabilities
  `p, q in [0, 1]`: `P[0] = [p, 1-p]`, `P[1] = [1-q, q]`.
- Output law: `q = P^T pi`; counts are `Multinomial(n, q)` per group.

Special cases:

- `k == 1`: channel `[[1.0]]`, output constant, the only distribution is
  `[1.0]`; estimation is trivial.
- `epsilon == 0`: all rows equal `(1/k, ..., 1/k)`; output is independent of
  input and the inverse problem is **unidentifiable** — no inverse is
  attempted; the unidentifiable (null-space) directions are reported instead.

## Estimation

Participants may use different known channels and are grouped by channel.
The joint estimate minimizes `sum_g n_g ||P_g^T pi - f_g||^2`. Three
estimates are reported side by side:

- `pi_unconstrained` — least-squares/inverse estimate; **may leave the
  simplex** (negative entries), reported as-is.
- `pi_clipped` — negatives clipped to 0 and renormalized; listed separately
  as a heuristic post-fix.
- `pi_constrained` — projected-gradient minimizer constrained to
  non-negativity and sum-to-one.

Diagnostics: per-group residuals `P_g^T pi_hat - f_g` and the 2-norm
condition number of the `sqrt(n_g)`-weighted stacked design matrix
(`inf` when rank deficient).

## Confidence intervals — coverage assumptions and limits

Wald intervals for the **unconstrained** estimator. Coverage assumes:
(1) i.i.d. responses within each group; (2) exactly known channel matrices;
(3) sample sizes large enough for the multinomial normal approximation;
(4) plug-in covariance (uses `pi_hat` for the true `pi`). Intervals may
extend outside `[0, 1]` and are **not available** for degenerate /
rank-deficient designs (e.g. `epsilon = 0`).

## Exact small-domain reference

`exact.exact_estimator_moments(P, pi, n)` enumerates all compositions of `n`
into `k` parts (feasible only for small `n, k`) to obtain the exact mean,
variance and bias of the inverse estimator — used to verify unbiasedness
(exact bias = 0 up to floating point) and to validate the asymptotic
covariance formula without Monte Carlo error.

## Running

```
python3 demo.py                  # demonstration scenarios
python3 -m unittest test_rr -v   # 30 unit tests
```
