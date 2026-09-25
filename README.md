# sptrisolve

Sparse triangular solver in pure Python 3.11 (standard library only, no
NumPy/SciPy), with dependency-driven level scheduling, shared analysis across
right-hand sides, transpose views, and incremental re-analysis after
structural changes.

## Features

- **Lower / upper / unit-diagonal / transpose solves.** `TriangularMatrix`
  takes the dimension `n`, triangle and diagonal convention as explicit
  inputs. `A.T` is a zero-copy view: a lower matrix is solved as upper and
  vice versa.
- **Dependency-determined order.** The substitution order is compiled from
  the dependency graph of the structure, never from the COO storage order.
  Nodes are grouped into levels; all nodes in one level are independent and
  may be evaluated in parallel (`analysis.levels`, `analysis.parallel_widths()`,
  `analysis.dependency_evidence()`).
- **Shared analysis for multiple RHS.** `solve_many` compiles the schedule
  once and applies it to every right-hand side. `matrix.stats()` reports
  builds, reuses and the analysis reuse rate.
- **Explicit pivot failures, kept apart.** A diagonal entry absent from the
  structure raises `MissingPivotError` (structural, at construction); a
  numerically zero pivot raises `ZeroPivotError` (numerical, at solve time).
  A failed solve raises and never publishes a partial solution.
- **Local invalidation after structural changes.** `insert` / `remove` (and
  `set_value` to exactly zero) update only the affected nodes of every
  compiled analysis (the changed node plus its transitive dependents);
  `analysis.last_recomputed` and `stats()["nodes_recomputed"]` give the
  evidence. Purely numeric updates (`set_value`, `set_diagonal`) keep the
  analysis valid.
- **Strict structural validation.** Duplicate coordinates are merged by
  summation first; entries outside the declared triangle raise
  `NotTriangularError` instead of being silently ignored; out-of-range or
  non-integer indices and ragged COO input raise explicit errors.

## Layout

```
sptrisolve/
  matrix.py     TriangularMatrix + TransposeView (build, mutate, solve)
  analysis.py   SolveAnalysis: level scheduling, incremental updates
  dense.py      self-written dense reference used for verification
  errors.py     exception hierarchy
tests/test_sptrisolve.py   unittest suite
examples/demo.py           runnable end-to-end demo
```

## Usage

```python
from sptrisolve import TriangularMatrix

m = TriangularMatrix.from_coo(
    3,
    rows=[0, 1, 2, 2],
    cols=[0, 1, 2, 0],
    values=[2.0, 1.0, 4.0, -1.0],
    lower=True,
)
x = m.solve([2.0, 3.0, 5.0])          # forward substitution
xt = m.T.solve([2.0, 3.0, 5.0])       # lower^T solved as upper
xs = m.solve_many([[1, 1, 1], [2, 2, 2]])  # one shared analysis

ana = m.analysis()
ana.levels                # [[0], [1], [2]] — parallelisable nodes per level
ana.dependency_evidence() # {0: [], 1: [0], 2: [0]} — dependency evidence
m.stats()                 # includes analysis_reuse_rate

m.insert(2, 1, 0.5)       # structural change: partial re-analysis only
m.set_value(2, 0, -2.0)   # numeric change: analysis stays valid
```

## Running

```
python3.11 -m unittest discover -s tests -v
python3.11 examples/demo.py
```

The tests generate random triangular matrices with shuffled storage order and
injected duplicate coordinates, and check every solve against the self-written
dense reference in `sptrisolve/dense.py` (plus a dense residual check).
