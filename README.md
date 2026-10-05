# fem_assembly

C++17 header-only finite element assembly library. Local rational element
matrices are scattered into a global sparse matrix through a DOF map, with
exact rational arithmetic, atomic batch validation, and prescribed-
displacement elimination that corrects the right-hand side.

## Layout

- `include/fem/rational.hpp` — exact rational arithmetic (reduced, sign-normalized, 128-bit intermediates)
- `include/fem/sparse_matrix.hpp` — dictionary-of-keys sparse matrix of rationals; cancelled entries are pruned
- `include/fem/assembler.hpp` — `Assembler`, `ElementContribution`, `AssemblyError`
- `tests/dense_reference.hpp` — independent dense reference used to cross-check the sparse path
- `tests/test_assembly.cpp` — unit tests

## Semantics

- `Assembler::addBatch` validates every contribution first (square local
  matrix, matching load/map sizes, in-range and duplicate-free DOF map).
  Any illegal mapping rejects the whole batch; state is left untouched.
- Repeated contributions to the same global entry accumulate exactly as
  rationals — no floating point drift.
- `prescribe(dof, g)` + `eliminateConstraints()` applies, for each
  constrained DOF `d`: `b_j -= K_jd * g` on unconstrained rows, zeroes
  row/column `d`, then sets `K_dd = 1`, `b_d = g`. Corrections use the
  pre-elimination stiffness, so mutually constrained (coupled) DOFs are
  handled consistently regardless of prescription order.
- DOFs untouched by any element keep exactly zero rows/columns.

## Build and test

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
