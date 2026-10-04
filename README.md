# fem-assembly

Header-only C++17 finite element assembly library: maps local element
matrices into a global sparse matrix via dof maps, with support for
shared dofs, prescribed-displacement (Dirichlet) elimination, and
all-or-nothing batch insertion.

## Layout

- `include/fem/assembly.hpp` — the whole library (`SparseMatrix<T>`,
  `ElementContribution<T>`, `Assembler<T>`)
- `tests/test_assembly.cpp` — dense-reference cross-checks
- `CMakeLists.txt` — interface target + ctest

## Semantics

- **Assembly**: `Assembler::add` / `addBatch` accumulate local square
  matrices and rhs vectors into the global system. Repeated
  contributions accumulate exactly (`+=` on the scalar type).
- **Validation**: dof maps must match local dimensions, stay in range,
  and contain no duplicates. `addBatch` validates every contribution
  before applying any, so an invalid mapping rejects the whole batch and
  leaves global state untouched.
- **Dirichlet elimination**: `applyDirichlet({{dof, value}, ...})`
  first moves column couplings to the right-hand side
  (`rhs[j] -= A(j,i) * g` for every constrained dof `i`), computed from
  the pre-elimination matrix so couplings between constrained dofs are
  consistent, then zeroes the rows/columns and sets `A(i,i) = 1`,
  `rhs[i] = g`.
- **Zero rows**: `SparseMatrix::zeroRows()` detects unconnected dofs;
  `Assembler::regularizeZeroRows()` inserts unit diagonal entries so
  the system stays solvable.

## Build & test

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build
```
