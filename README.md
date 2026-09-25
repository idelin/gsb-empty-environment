# Restarted GMRES(m) — pure Python 3.11

Restarted GMRES for real linear systems `A x = b`, standard library only
(no NumPy/SciPy). The matrix is accessed exclusively through a
`matvec(x)` callback, so `A` can be dense, sparse, or matrix-free.

## Files

- `gmres.py` — solver (`gmres()`, `GmresResult`, error types)
- `test_gmres.py` — 16 unittest cases, incl. dense Gaussian-elimination cross-check
- `example.py` — runnable demo (matrix-free 1D Poisson)

## Usage

```python
from gmres import gmres

res = gmres(matvec, b, n=n, restart=30, tol=1e-9, maxiter=500)
res.converged          # bool — never faked
res.reason             # "converged" | "happy_breakdown" | "zero_rhs"
                       # | "stagnation" | "budget_exhausted"
res.residual_history   # Arnoldi (Givens) residual estimate per inner step
res.cycle_residuals    # residual estimate at the end of each restart cycle
res.restart_cycles     # number of restarts actually performed
res.true_residual      # ||b - A x|| measured with a fresh matvec
res.peak_krylov_vectors  # <= restart + 1 (working-memory bound)
```

## Guarantees

- **Explicit dimensions/indices**: `n` is a required keyword; `b`, `x0`
  and every matvec result are length-checked, and errors name the exact
  index and location, e.g.
  `matvec output y[1] = nan is not finite at restart cycle 0, Arnoldi step 0`.
- **Memory bounded by restart length**: at most `restart + 1` Krylov
  vectors of length `n` plus an `(m+1) x m` Hessenberg matrix are alive.
- **Zero rhs**: returns `x0` immediately (`reason="zero_rhs"`, 0 matvecs).
- **Honest convergence**: budget exhaustion reports
  `converged=False, reason="budget_exhausted"`; every claimed
  convergence is verified against the true residual `||b - A x||`.
- **Stagnation**: a restart cycle with relative residual improvement
  below `stagnation_rtol` (or a singular Arnoldi breakdown) stops with
  `reason="stagnation"`.

## Sample run (`python3 example.py`)

```
--- Poisson n=64, restart=30, tol=1e-9 ---
converged        : True  (reason: converged)
iterations       : 450 Arnoldi steps, 14 restart cycles, 466 matvecs
peak memory      : 31 Krylov vectors
true residual    : 7.59973486637966e-09
Arnoldi residuals: [7.874007874011811, 7.745966692414834, 7.615773105863909, 7.483314773547883, '...', 1.181058313193826e-08, 1.0723591026064765e-08, 8.972345588293108e-09, 7.599599016779481e-09]
cycle residuals  : [2.000000000000014, 0.4999999999999698, 0.12500000000001912, 0.03125000000000736, 0.007812499999982255, 0.0019531249999933538, 0.0004882812500062344, 0.00012207031256479065, 3.051784936983872e-05, 7.629895355298803e-06, 1.914427839336249e-06, 4.804176530991932e-07, 1.2058573034336155e-07, 3.0269823087066866e-08]

--- zero right-hand side ---
converged        : True  (reason: zero_rhs)
iterations       : 0 Arnoldi steps, 0 restart cycles, 0 matvecs

--- budget exhausted (maxiter=3) ---
converged        : False  (reason: budget_exhausted)
iterations       : 3 Arnoldi steps, 0 restart cycles, 5 matvecs
true residual    : 7.615773105863909
```

## Test results (`python3 -m unittest -v`)

```
Ran 16 tests in 0.006s

OK
```

Covers: dense-solve cross-check, monotone residual history, restart/memory
bounds, budget honesty, zero rhs, warm start, happy breakdown, both
stagnation paths, and located errors for wrong-size / non-finite callback
output and inputs.
